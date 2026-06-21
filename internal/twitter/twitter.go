// Package twitter fetches a single tweet: yt-dlp first (video tweets), falling
// back to the public syndication endpoint (text + image tweets). All network
// goes through the supplied proxy (X is blocked on the bot's egress).
package twitter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"video-to-notes/internal/douyin"
	"video-to-notes/internal/source"
)

// minTweetText is the floor (in runes) below which a text-only tweet is rejected:
// too little real content to write a faithful article (would just be fabricated).
const minTweetText = 50

var idRe = regexp.MustCompile(`/status/(\d+)`)

func tweetID(rawURL string) (string, error) {
	m := idRe.FindStringSubmatch(rawURL)
	if m == nil {
		return "", fmt.Errorf("no /status/{id} in %q", rawURL)
	}
	return m[1], nil
}

// synToken mirrors react-tweet: ((id/1e15)*PI) in base36, with '0' and '.' stripped.
func synToken(id string) string {
	n, _ := strconv.ParseFloat(id, 64)
	v := (n / 1e15) * math.Pi
	s := base36(v)
	s = strings.ReplaceAll(s, "0", "")
	s = strings.ReplaceAll(s, ".", "")
	return s
}

func base36(f float64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	i := int64(f)
	frac := f - float64(i)
	var ip string
	if i == 0 {
		ip = "0"
	}
	for i > 0 {
		ip = string(digits[i%36]) + ip
		i /= 36
	}
	var fp strings.Builder
	for k := 0; k < 16 && frac > 0; k++ {
		frac *= 36
		d := int(frac)
		fp.WriteByte(digits[d])
		frac -= float64(d)
	}
	return ip + "." + fp.String()
}

var tcoRe = regexp.MustCompile(`https?://t\.co/\w+`)

// parseSyndication extracts the best available text, image URLs, author, and
// whether the tweet is an X long-form Article. For Articles the endpoint exposes
// only title + a short preview (the full body needs login), so isArticle lets the
// caller refuse to fabricate a full article from a teaser.
func parseSyndication(jsonBytes []byte) (text string, images []string, author string, isArticle bool, err error) {
	var t struct {
		Text string `json:"text"`
		User struct {
			ScreenName string `json:"screen_name"`
		} `json:"user"`
		MediaDetails []struct {
			Type          string `json:"type"`
			MediaURLHTTPS string `json:"media_url_https"`
		} `json:"mediaDetails"`
		Article *struct {
			Title       string `json:"title"`
			PreviewText string `json:"preview_text"`
			CoverMedia  struct {
				MediaInfo struct {
					OriginalImgURL string `json:"original_img_url"`
				} `json:"media_info"`
			} `json:"cover_media"`
		} `json:"article"`
	}
	if err = json.Unmarshal(jsonBytes, &t); err != nil {
		return "", nil, "", false, fmt.Errorf("parse syndication json: %w", err)
	}
	author = t.User.ScreenName
	for _, m := range t.MediaDetails {
		if m.Type == "photo" && m.MediaURLHTTPS != "" {
			images = append(images, m.MediaURLHTTPS)
		}
	}
	if t.Article != nil {
		isArticle = true
		text = strings.TrimSpace(t.Article.Title + "\n" + t.Article.PreviewText)
		if u := t.Article.CoverMedia.MediaInfo.OriginalImgURL; u != "" {
			images = append(images, u)
		}
	} else {
		text = t.Text
	}
	// Drop bare t.co links so a link-only tweet reads as empty (caught by the guard).
	text = strings.TrimSpace(tcoRe.ReplaceAllString(text, ""))
	return text, images, author, isArticle, nil
}

// Fetch tries yt-dlp (video tweets) then the syndication endpoint (text+image
// tweets). For X long-form Articles, whose full body is login-walled, it uses the
// authenticated GraphQL endpoint when auth cookies are configured. All HTTP/yt-dlp
// traffic uses proxy.
func Fetch(rawURL, proxy, tmpDir, authToken, ct0 string) (source.Item, error) {
	id, err := tweetID(rawURL)
	if err != nil {
		return source.Item{}, err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return source.Item{}, err
	}
	meta := source.Meta{SourceURL: rawURL, ID: id}

	// 1) yt-dlp — succeeds only when the tweet has video.
	if item, ok := fetchVideo(rawURL, proxy, tmpDir, meta); ok {
		return item, nil
	}

	// 2) syndication — text + images (+ detects X Articles).
	text, images, author, isArticle, err := fetchSyndication(id, proxy)
	if err != nil {
		return source.Item{}, fmt.Errorf("twitter fallback: %w", err)
	}
	meta.Author = author
	meta.Title = firstLine(text)

	// X Articles: syndication gives only title+preview. Fetch the full body via the
	// authenticated GraphQL endpoint; without cookies, skip rather than fabricate.
	if isArticle {
		if authToken == "" || ct0 == "" {
			return source.Item{}, fmt.Errorf("X 长文需登录 cookie（未配置 TWITTER_AUTH_TOKEN/TWITTER_CT0）")
		}
		full, err := fetchArticlePlainText(id, authToken, ct0, proxy)
		if err != nil {
			return source.Item{}, fmt.Errorf("X 长文抓取失败（cookie 可能已过期，请更新）：%w", err)
		}
		text = strings.TrimSpace(meta.Title + "\n\n" + full)
	}

	var paths []string
	for i, u := range images {
		p := filepath.Join(tmpDir, fmt.Sprintf("%s-%d.jpg", id, i))
		if err := downloadFile(u, p, proxy); err == nil {
			paths = append(paths, p)
		}
	}
	// Guard: without enough real text and no media, analysis would just hallucinate.
	if len([]rune(text)) < minTweetText && len(paths) == 0 {
		return source.Item{}, fmt.Errorf("推文内容过少（%d 字、无媒体），跳过以免生成不实内容", len([]rune(text)))
	}
	kind := ""
	if len(paths) > 0 {
		kind = "image"
	}
	return source.Item{Kind: "twitter", Meta: meta, MediaPaths: paths, MediaKind: kind, Text: text}, nil
}

func fetchVideo(rawURL, proxy, tmpDir string, meta source.Meta) (source.Item, bool) {
	outTmpl := filepath.Join(tmpDir, "%(id)s.%(ext)s")
	var stdout bytes.Buffer
	args := []string{"--no-playlist", "--no-progress", "-S", "+size", "--print-json", "-o", outTmpl}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}
	args = append(args, rawURL)
	cmd := exec.Command("yt-dlp", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return source.Item{}, false // no video / yt-dlp failed → caller falls back
	}
	var info struct {
		ID, Ext, Title, Uploader, UploaderID, WebpageURL string
	}
	if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info) != nil {
		return source.Item{}, false
	}
	src := filepath.Join(tmpDir, info.ID+"."+info.Ext)
	defer os.Remove(src)
	dst := filepath.Join(tmpDir, info.ID+".small.mp4")
	if err := douyin.Transcode(src, dst); err != nil {
		return source.Item{}, false
	}
	meta.Author = info.UploaderID
	meta.Title = info.Title
	return source.Item{Kind: "twitter", Meta: meta, MediaPaths: []string{dst}, MediaKind: "video"}, true
}

// X web app public bearer + the TweetResultByRestId query.
// ponytail: articleQueryID/articleFeatures track X's web build and may rotate; if
// Article fetches start 404/400-ing, recapture them from a logged-in browser's
// network tab. plain_text needs only auth cookies — no x-client-transaction-id.
const (
	webBearer           = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs=1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
	articleQueryID      = "8CEYnZhCp0dx9DFyyEBlbQ"
	articleFeatures     = `%7B%22creator_subscriptions_tweet_preview_api_enabled%22%3Atrue%2C%22premium_content_api_read_enabled%22%3Afalse%2C%22communities_web_enable_tweet_community_results_fetch%22%3Atrue%2C%22c9s_tweet_anatomy_moderator_badge_enabled%22%3Atrue%2C%22responsive_web_grok_analyze_button_fetch_trends_enabled%22%3Afalse%2C%22responsive_web_grok_analyze_post_followups_enabled%22%3Atrue%2C%22rweb_cashtags_composer_attachment_enabled%22%3Atrue%2C%22responsive_web_jetfuel_frame%22%3Atrue%2C%22responsive_web_grok_share_attachment_enabled%22%3Atrue%2C%22responsive_web_grok_annotations_enabled%22%3Atrue%2C%22articles_preview_enabled%22%3Atrue%2C%22responsive_web_edit_tweet_api_enabled%22%3Atrue%2C%22rweb_conversational_replies_downvote_enabled%22%3Afalse%2C%22graphql_is_translatable_rweb_tweet_is_translatable_enabled%22%3Atrue%2C%22view_counts_everywhere_api_enabled%22%3Atrue%2C%22longform_notetweets_consumption_enabled%22%3Atrue%2C%22responsive_web_twitter_article_tweet_consumption_enabled%22%3Atrue%2C%22content_disclosure_indicator_enabled%22%3Atrue%2C%22content_disclosure_ai_generated_indicator_enabled%22%3Atrue%2C%22responsive_web_grok_show_grok_translated_post%22%3Atrue%2C%22responsive_web_grok_analysis_button_from_backend%22%3Atrue%2C%22post_ctas_fetch_enabled%22%3Afalse%2C%22rweb_cashtags_enabled%22%3Atrue%2C%22freedom_of_speech_not_reach_fetch_enabled%22%3Atrue%2C%22standardized_nudges_misinfo%22%3Atrue%2C%22tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled%22%3Atrue%2C%22longform_notetweets_rich_text_read_enabled%22%3Atrue%2C%22longform_notetweets_inline_media_enabled%22%3Afalse%2C%22profile_label_improvements_pcf_label_in_post_enabled%22%3Atrue%2C%22responsive_web_profile_redirect_enabled%22%3Afalse%2C%22rweb_tipjar_consumption_enabled%22%3Afalse%2C%22verified_phone_label_enabled%22%3Afalse%2C%22responsive_web_grok_image_annotation_enabled%22%3Atrue%2C%22responsive_web_grok_imagine_annotation_enabled%22%3Atrue%2C%22responsive_web_grok_community_note_auto_translation_is_enabled%22%3Atrue%2C%22responsive_web_graphql_skip_user_profile_image_extensions_enabled%22%3Afalse%2C%22responsive_web_graphql_timeline_navigation_enabled%22%3Atrue%7D`
	articleFieldToggles = `%7B%22withArticleRichContentState%22%3Afalse%2C%22withArticlePlainText%22%3Atrue%2C%22withArticleSummaryText%22%3Atrue%2C%22withArticleVoiceOver%22%3Afalse%7D`
)

// fetchArticlePlainText gets an X Article's full body via the authenticated
// GraphQL endpoint (plain_text field). Needs auth_token + ct0 cookies.
func fetchArticlePlainText(tweetID, authToken, ct0, proxy string) (string, error) {
	variables := url.QueryEscape(`{"tweetId":"` + tweetID + `","includePromotedContent":true,"withBirdwatchNotes":true,"withVoice":true,"withCommunity":true}`)
	endpoint := fmt.Sprintf("https://x.com/i/api/graphql/%s/TweetResultByRestId?variables=%s&features=%s&fieldToggles=%s",
		articleQueryID, variables, articleFeatures, articleFieldToggles)

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+webBearer)
	req.Header.Set("x-csrf-token", ct0)
	req.Header.Set("x-twitter-auth-type", "OAuth2Session")
	req.Header.Set("x-twitter-active-user", "yes")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Cookie", "auth_token="+authToken+"; ct0="+ct0)

	resp, err := proxyClient(proxy).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("graphql HTTP %d", resp.StatusCode)
	}
	var j any
	if err := json.Unmarshal(body, &j); err != nil {
		return "", fmt.Errorf("decode graphql: %w", err)
	}
	pt := findString(j, "plain_text")
	if pt == "" {
		return "", fmt.Errorf("plain_text not found in response")
	}
	return pt, nil
}

// findString returns the first string value under key anywhere in the decoded
// JSON tree (the article body nests deep and the path shifts between builds).
func findString(o any, key string) string {
	switch v := o.(type) {
	case map[string]any:
		if s, ok := v[key].(string); ok && s != "" {
			return s
		}
		for _, val := range v {
			if s := findString(val, key); s != "" {
				return s
			}
		}
	case []any:
		for _, val := range v {
			if s := findString(val, key); s != "" {
				return s
			}
		}
	}
	return ""
}

func fetchSyndication(id, proxy string) (text string, images []string, author string, isArticle bool, err error) {
	endpoint := fmt.Sprintf("https://cdn.syndication.twimg.com/tweet-result?id=%s&token=%s&lang=en", id, synToken(id))
	body, err := httpGet(endpoint, proxy)
	if err != nil {
		return "", nil, "", false, err
	}
	return parseSyndication(body)
}

func proxyClient(proxy string) *http.Client {
	tr := &http.Transport{}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

func httpGet(u, proxy string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := proxyClient(proxy).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func downloadFile(u, dst, proxy string) error {
	b, err := httpGet(u, proxy)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(strings.TrimSpace(s)); len(r) > 30 {
		return string(r[:30])
	}
	return strings.TrimSpace(s)
}
