# 设计：可持久化任务队列

**日期**：2026-06-16
**目标**：让 Telegram bot 的任务队列在容器重启 / 崩溃 / 重新部署后自动恢复，排队中与处理中途的任务不再静默丢失。

---

## 背景

当前队列是纯内存（`chan job` + 单 worker，main.go）。产出（笔记/文章）已通过 git 持久化，但**队列本身没落盘**：一旦进程重启，排队中和处理中途的任务全部丢失，用户那条「已加入队列」消息会永远停住。Telegram 也不会重发（`WithInitialOffset(-1)` 跳过积压 + offset 入队即前移）。

## 选型

- **A. JSONL 追加事件日志（采用）**：一个文件，每次状态变化追加一行。零依赖、追加写抗崩溃、代码最少。
- B. SQLite（纯 Go）：过重，单队列不值得引入依赖。
- C. 每任务一文件：目录扫描/churn 多。

YAGNI → A。

## 架构

新包 `internal/jobqueue`：

```go
type Event string // "queued" | "done" | "failed"

type Record struct {
    ID       string `json:"id"`                  // chatID:statusID
    Event    Event  `json:"event"`
    ChatID   int64  `json:"chat_id,omitempty"`   // 仅 queued 需要
    StatusID int    `json:"status_id,omitempty"`
    URL      string `json:"url,omitempty"`
    Time     string `json:"time,omitempty"`
}

type Job struct { ID string; ChatID int64; StatusID int; URL string }

type Store struct { path string; mu sync.Mutex }

func Open(path string) (*Store, error)            // 确保目录存在
func (s *Store) Append(r Record) error            // 加锁，O_APPEND 写一行
func (s *Store) MarkQueued(j Job) error           // Append(queued)
func (s *Store) MarkDone(id string) error         // Append(done)
func (s *Store) MarkFailed(id string) error       // Append(failed)
func (s *Store) LoadPending() ([]Job, error)      // 回放：有 queued 无终态
func (s *Store) Compact(pending []Job) error      // 用 pending 重写文件（原子：写临时文件 + rename）
```

- **存储位置**：`${TMP_DIR}/queue.jsonl`。compose 已把 `./tmp` 挂到宿主，重启后仍在；且不在 git 仓库内。
- **Job ID**：`fmt.Sprintf("%d:%d", chatID, statusID)`，每条回执消息唯一。
- **回放规则**：扫全部记录，按 ID 聚合；ID 出现过 `queued` 且没出现 `done`/`failed` → pending。保持首次 queued 里的 ChatID/StatusID/URL。
- **Compact**：`LoadPending` 后用纯 pending 的 queued 行重写文件（写 `queue.jsonl.tmp` 再 `os.Rename`），避免日志无限增长。

## 数据流（main.go 改动）

- `app` 增加 `store *jobqueue.Store`；`job` 增加 `id string`。
- `handle`：解析链接 → 发「✅ 已加入队列」回执拿 statusID → `store.MarkQueued(job)` → `a.jobs <- job`（先落盘再入队）。
- `process` 改为返回 `error`（nil=成功，非 nil=已上报 ❌ 的失败）。`worker` 在 `process` 返回后：`err==nil → store.MarkDone(id)`，否则 `store.MarkFailed(id)`。终态写入集中在 worker 一处，process 内部仍负责编辑 ❌ 消息。
- **启动恢复**（`b.Start` 前）：
  1. `pending, _ := store.LoadPending()`
  2. `store.Compact(pending)`
  3. 对每个 pending：`atomic.AddInt64(&a.queued,1)`；把其原回执消息编辑成「♻️ 重启后恢复，排队中…」；`a.jobs <- j`
  4. 再 `go a.worker(ctx)`，`b.Start(ctx)`

## 关键语义

- **只有崩溃在处理中途**（未写终态）的任务会在下次启动重试。
- 已上报 ❌ 的硬失败 → 写 `failed` 终态 → **不重试**（否则永久失败任务每次重启重跑）。
- 重跑会重新生成文章（LLM 非确定性 → 有 diff → git 正常提交）。
- 持久化是「尽力而为」：`Append`/`MarkDone` 出错只 **大声 log**，不阻断主流程（内存队列仍跑），避免因落盘问题吞掉任务。

## 测试

`Store` 全是纯文件操作，可完整单测，无需网络：
- `Append(queued)×N → LoadPending` 返回全部 N 个、字段正确。
- 写 `done`/`failed` 后，该 ID 不再出现在 `LoadPending`。
- 同一 ID 多次 queued 去重（理论上不会，但回放要稳）。
- `Compact(pending)` 后文件只剩 pending，再 `LoadPending` 仍一致。
- 文件不存在时 `LoadPending` 返回空、无错误。

## 验证 / 发布

- 单测绿、`go vet`/`go build` 绿。
- 部署后做一次重启恢复实测：构造一条 pending（或发任务后立刻重启容器），确认重启后 worker 自动续跑、原消息更新到 ✅。
- 交叉编译 linux/amd64 → `docker compose up -d --build`。
