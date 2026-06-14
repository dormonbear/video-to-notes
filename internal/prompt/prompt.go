package prompt

// VideoNote 指示模型听完视频音频后输出结构化笔记。
// 实际结构由 response_format 的 json_schema 强制，prompt 只描述任务与语言。
const VideoNote = `你是一个视频笔记助手。这是一段视频的音频，请听完后用中文输出：
1. summary：一句话概括主旨。
2. tags：3-6 个主题标签（不带 # 号，简短名词）。
3. key_points：核心要点/重点，每条一句，按讲述顺序。
4. transcript：尽量完整的口语转写文字稿（去掉语气词、修正明显口误，保留原意）。
严格按要求的 JSON schema 输出，不要输出多余文字。`
