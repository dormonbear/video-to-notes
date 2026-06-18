# 可持久化任务队列 实现计划

> 执行：TDD，按任务推进。

**Goal**：队列落盘到 `${TMP_DIR}/queue.jsonl`，重启自动恢复未完成任务。

**Tech**：Go，新增 `internal/jobqueue`（JSONL 事件日志，零依赖）；改 `main.go` 集成。

---

## Task 1：jobqueue 包（TDD）

**Files**
- Create: `internal/jobqueue/jobqueue.go`
- Test: `internal/jobqueue/jobqueue_test.go`

- [ ] 写测试：`Open` 临时文件 → `MarkQueued(j1,j2,j3)` → `LoadPending` 返回 3 个、字段正确。
- [ ] 写测试：`MarkDone(j2.ID)`、`MarkFailed(j3.ID)` 后 `LoadPending` 只剩 j1。
- [ ] 写测试：`Compact(pending)` 后文件只剩 pending；再 `LoadPending` 一致。
- [ ] 写测试：文件不存在时 `LoadPending` 返回空、nil error。
- [ ] 实现 `jobqueue.go`：
  - `Event`、`Record`、`Job`、`Store{path,mu}`
  - `Open(path)`：`os.MkdirAll(filepath.Dir(path))`，返回 `&Store{path}`
  - `Append(Record)`：mutex + `os.OpenFile(O_APPEND|O_CREATE|O_WRONLY)` 写一行 JSON
  - `MarkQueued(Job)`/`MarkDone(id)`/`MarkFailed(id)`
  - `LoadPending()`：读全文件按行解析；map[id]→首个 queued 的 Job；遇 done/failed 从 map 删；返回剩余（按首次出现顺序）
  - `Compact(pending)`：写 `path+".tmp"` 每个 pending 一行 queued，`os.Rename` 覆盖
- [ ] `go test ./internal/jobqueue/` 绿。

## Task 2：集成进 main.go

**Files**: Modify `main.go`

- [ ] `job` 加 `id string`。
- [ ] `app` 加 `store *jobqueue.Store`。
- [ ] main：`store, err := jobqueue.Open(filepath.Join(cfg.TmpDir,"queue.jsonl"))`；放进 app。
- [ ] `handle`：每个 url → 发回执拿 statusID → `id := fmt.Sprintf("%d:%d",chatID,status.ID)` → `j := job{id,chatID,status.ID,u}` → `store.MarkQueued(...)`（失败只 log）→ `a.jobs <- j`。
- [ ] `process` 改签名返回 `error`：各失败分支 `edit(...)` 后 `return err`；成功 `return nil`。
- [ ] `worker`：`err := a.process(...)`；`if err==nil { store.MarkDone(j.id) } else { store.MarkFailed(j.id) }`（失败只 log）；`atomic.AddInt64(&a.queued,-1)`。
- [ ] 启动恢复（`go a.worker` 前）：`pending,_ := store.LoadPending()`；`store.Compact(pending)`；遍历：`atomic.AddInt64(&a.queued,1)`；编辑原消息为「♻️ 重启后恢复，排队中…」；`a.jobs <- j`。
- [ ] `go vet ./... && go build ./...` 绿。

## Task 3：发布 + 重启恢复实测

- [ ] 交叉编译 linux/amd64 → ship → `docker compose up -d --build`。
- [ ] 实测恢复：直接往宿主 `tmp/queue.jsonl` 写一条 queued（用一个真实视频 URL + 一个已存在的 chat 的假 statusID 或 selftest 路径），重启容器，确认 worker 自动消费续跑。
- [ ] bot 正常启动、日志无报错。
