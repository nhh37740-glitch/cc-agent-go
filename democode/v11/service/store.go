package service

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cc-agent-go/democode/v11/model"
)

// ============================================================================
// Store —— 会话持久化管理
// ============================================================================
//
// Store 负责会话 JSON 文件的读写、会话列表、删除、以及超长对话的 LLM 压缩。
// 采用 per-conversation 互斥锁：不同会话之间可以并发读写，同一会话的并发请求串行化。
//
// Go 概念速查：
//   sync.Mutex   — 互斥锁，Lock() 加锁、Unlock() 解锁，等价 Java synchronized
//   defer        — 延迟执行，写在 Lock() 后面保证函数退出时一定 Unlock()
//   os.MkdirAll  — 递归创建目录，等价 mkdir -p
//   os.ReadFile  — Go 1.16+ 读文件到 []byte
//   os.WriteFile — Go 1.16+ 写 []byte 到文件
//   json.MarshalIndent — 带缩进的 JSON 序列化，第二个参数是前缀，第三个是缩进
//   sort.Slice   — 切片原地排序
//   regexp.MustCompile — 编译正则，Must 前缀表示编译失败时 panic

type Store struct {
	sessionsDir string
	mu          sync.Mutex             // 保护 locks map 本身的并发访问
	locks       map[string]*sync.Mutex // 每个 conversationId 一个锁
}

// ========================================================================
// 构造函数
// ========================================================================

// NewStore 创建 Store 实例并确保 sessions 目录存在。
// os.MkdirAll(path, perm) 递归创建目录：
//   - path: 目录路径
//   - perm: 文件权限，0755 = rwxr-xr-x（所有者读写执行，组/其他只读执行）
//
// 目录已存在时不报错。
func NewStore(sessionsDir string) *Store {
	os.MkdirAll(sessionsDir, 0755)
	return &Store{
		sessionsDir: sessionsDir,
		locks:       make(map[string]*sync.Mutex),
	}
}

// ========================================================================
// Memory 加载
// ========================================================================

// LoadMemory 读取 AGENT.MD 文件内容。文件不存在时返回空字符串（不是错误），
// 这样首次启动时无需手动创建文件。
//
// os.ReadFile 在文件不存在时返回 *PathError，os.IsNotExist(err) 判断
// 是否为"文件不存在"错误。
func LoadMemory(memoryPath string) (string, error) {
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // 文件不存在 = 尚无记忆，不报错
		}
		return "", fmt.Errorf("读取 memory 文件失败: %w", err)
	}
	return string(data), nil
}

// ========================================================================
// 会话 ID 校验
// ========================================================================

// safeIdPattern 和 Java cc-agent-java 的 SAFE_CONVERSATION_ID 正则一致，
// 只允许字母、数字、点、下划线、连字符，长度 1-100。防止路径遍历攻击。
var safeIdPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

func validateConversationId(id string) error {
	if !safeIdPattern.MatchString(id) {
		return fmt.Errorf("无效的 conversationId: %s（只允许字母数字 . _ -，最长 100 字符）", id)
	}
	return nil
}

// ========================================================================
// 内部辅助函数
// ========================================================================

// sessionPath 返回会话 JSON 文件的完整路径。
func (s *Store) sessionPath(conversationId string) string {
	return filepath.Join(s.sessionsDir, conversationId+".json")
}

// archivePath 返回归档文件的完整路径（压缩时旧消息移到这里）。
func (s *Store) archivePath(conversationId string) string {
	return filepath.Join(s.sessionsDir, conversationId+".archive.json")
}

// lockFor 获取或创建指定 conversationId 的互斥锁。
//
// 实现手法：先用 s.mu（全局锁）保护 locks map 的读写，查到/创建 per-conversation
// 锁后立即释放全局锁。调用方拿到 per-conversation 锁后自己 Lock/Unlock。
// 这等价于 Java 的 ConcurrentHashMap<String, Object> + synchronized(lockFor(id))。
func (s *Store) lockFor(conversationId string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock() // defer: 函数返回前一定执行，保证全局锁被释放

	if l, ok := s.locks[conversationId]; ok {
		return l
	}
	l := &sync.Mutex{}
	s.locks[conversationId] = l
	return l
}

// readSessionJson 从文件读取并反序列化 SessionJson。
func (s *Store) readSessionJson(conversationId string) (*model.SessionJson, error) {
	path := s.sessionPath(conversationId)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 会话不存在，返回 nil 而非 error
		}
		return nil, fmt.Errorf("读取会话文件失败: %w", err)
	}

	var sj model.SessionJson
	if err := json.Unmarshal(data, &sj); err != nil {
		return nil, fmt.Errorf("解析会话 JSON 失败: %w", err)
	}
	return &sj, nil
}

// writeSessionJson 将会话序列化为 JSON 并写入文件。
// json.MarshalIndent(v, prefix, indent) 的三个参数：
//   - v: 要序列化的值
//   - prefix: 每行前缀（通常空字符串）
//   - indent: 缩进字符串（两个空格）
func (s *Store) writeSessionJson(sj *model.SessionJson) error {
	data, err := json.MarshalIndent(sj, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话 JSON 失败: %w", err)
	}
	// 末尾加换行，和 Java 版格式一致
	data = append(data, '\n')

	path := s.sessionPath(sj.ConversationId)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入会话文件失败: %w", err)
	}
	return nil
}

// ========================================================================
// 公开 API
// ========================================================================

// LoadMessages 加载指定会话的历史消息。会话不存在时返回空切片。
func (s *Store) LoadMessages(conversationId string) ([]model.Message, error) {
	if err := validateConversationId(conversationId); err != nil {
		return nil, err
	}
	sj, err := s.readSessionJson(conversationId)
	if err != nil {
		return nil, err
	}
	if sj == nil {
		return []model.Message{}, nil // 新会话：返回空切片
	}
	return sj.Messages, nil
}

// LoadConversation 加载完整会话对象（含元数据）。用于 GET /api/conversations/{id}。
func (s *Store) LoadConversation(conversationId string) (*model.SessionJson, error) {
	if err := validateConversationId(conversationId); err != nil {
		return nil, err
	}
	return s.readSessionJson(conversationId)
}

// ListConversations 列出所有会话摘要，按 updatedAt 降序排列。
//
// sort.Slice(slice, less) 对切片原地排序：
//   - slice: 要排序的切片
//   - less: 比较函数 less(i, j) 返回 true 表示 i 应该排在 j 前面
//     这里返回 summaries[i].UpdatedAt > summaries[j].UpdatedAt 实现降序。
func (s *Store) ListConversations() ([]model.ConversationSummary, error) {
	entries, err := os.ReadDir(s.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []model.ConversationSummary{}, nil
		}
		return nil, fmt.Errorf("读取 sessions 目录失败: %w", err)
	}

	var summaries []model.ConversationSummary
	for _, entry := range entries {
		name := entry.Name()

		// 跳过目录和非 JSON 文件，以及归档文件
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		// strings.Contains 判断子串：归档文件 .archive.json 不列入列表
		if strings.Contains(name, ".archive.") {
			continue
		}

		path := filepath.Join(s.sessionsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sj model.SessionJson
		if err := json.Unmarshal(data, &sj); err != nil {
			// JSON 损坏时返回占位摘要（和 Java 行为一致）
			summaries = append(summaries, model.ConversationSummary{
				ConversationId: strings.TrimSuffix(name, ".json"),
				Title:          "（无法读取）",
			})
			continue
		}

		// 计算 token 窗口
		windowTokens := sj.LastInputTokens + sj.LastOutputTokens
		if windowTokens == 0 {
			windowTokens = sj.RunningTotalTokens
		}

		summaries = append(summaries, model.ConversationSummary{
			ConversationId:           sj.ConversationId,
			Title:                    sj.Title,
			UpdatedAt:                sj.UpdatedAt,
			ContextWindowLimitTokens: sj.ContextWindowLimitTokens,
			CurrentInputTokens:       sj.LastInputTokens,
			CurrentOutputTokens:      sj.LastOutputTokens,
			CurrentTotalTokens:       sj.RunningTotalTokens,
			CurrentWindowTokens:      windowTokens,
		})
	}

	// sort.Slice 原地排序，按更新时间降序（最新的排最前）
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt > summaries[j].UpdatedAt
	})

	// 避免返回 null：空列表时返回 [] 而非 nil
	if summaries == nil {
		summaries = []model.ConversationSummary{}
	}
	return summaries, nil
}

// DeleteConversation 删除指定会话的 JSON 文件和归档文件。
func (s *Store) DeleteConversation(conversationId string) error {
	if err := validateConversationId(conversationId); err != nil {
		return err
	}

	l := s.lockFor(conversationId)
	l.Lock()
	defer l.Unlock()

	// 删除主会话文件
	if err := os.Remove(s.sessionPath(conversationId)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除会话文件失败: %w", err)
	}
	// 删除归档文件（可能不存在）
	os.Remove(s.archivePath(conversationId))
	return nil
}

// ========================================================================
// AppendTurn —— 追加一轮对话（核心写入路径）
// ========================================================================
//
// 流程：
//  1. 读现有 SessionJson（不存在则创建初始对象）
//  2. 检查 runningTotalTokens + 本轮 tokens 是否超过压缩阈值
//  3. 超过阈值且提供了 compressor → 调 LLM 生成摘要 → 旧消息归档
//  4. 否则 → 普通追加（新消息拼到旧消息后面）
//  5. 写回 JSON 文件
//
// 压缩失败时 fallback 普通追加，不丢数据（和 Java 行为一致）。
//
// 参数:
//   - conversationId: 会话 ID
//   - newMessages: 本轮新增的消息（user + assistant + tool_result）
//   - totalOutputTokens: 本轮 API 输出的 token 数
//   - compressor: 压缩函数，nil 表示跳过压缩
func (s *Store) AppendTurn(conversationId string, newMessages []model.Message,
	totalOutputTokens int, compressor model.Compressor) error {

	if err := validateConversationId(conversationId); err != nil {
		return err
	}

	// per-conversation 锁：同一会话的并发请求串行化
	l := s.lockFor(conversationId)
	l.Lock()
	defer l.Unlock() // defer 保证函数无论如何退出都会解锁

	now := time.Now()
	nowInstant := javaInstant(now)

	// 读现有会话
	existing, err := s.readSessionJson(conversationId)
	if err != nil {
		return err
	}

	if existing == nil {
		// 新会话：创建初始 SessionJson
		title := firstUserText(newMessages)
		if title == "" {
			title = "未命名会话"
		}
		existing = &model.SessionJson{
			ConversationId:           conversationId,
			Title:                    title,
			RunningTotalTokens:       0,
			ContextWindowLimitTokens: 150000, // 和 Java 版一致
			CreatedAt:                nowInstant,
			Messages:                 []model.Message{},
		}
	}

	// 更新元数据
	existing.LastInputTokens = 0 // 由 agent 层后续更新（或保持为 0）
	existing.LastOutputTokens = totalOutputTokens
	existing.UpdatedAt = nowInstant

	// 判断是否需要压缩
	newRunningTotal := existing.RunningTotalTokens + totalOutputTokens

	// compressionThreshold 从调用方传入（通过 config），这里用 ContextWindowLimitTokens 的 2/3 作为默认阈值。
	// 实际阈值由 config.CompressionThreshold 控制，agent 层在调用 AppendTurn 前判断。
	// 此处做实际检查：如果 compressor 不为 nil 且超过阈值且有旧消息需要压缩，
	// 则由调用方在 AppendTurn 前完成压缩判断。为简化，这里让调用方决定是否压缩。

	// 普通追加：新旧消息拼接
	allMessages := append(existing.Messages, newMessages...)
	existing.Messages = allMessages
	existing.RunningTotalTokens = newRunningTotal

	// 写回文件
	return s.writeSessionJson(existing)
}

// AppendTurnWithCompression 在 AppendTurn 基础上增加压缩逻辑。
// 当 runningTotalTokens + totalOutputTokens > threshold 且旧消息非空时，
// 调用 compressor 生成摘要，旧消息归档，会话以摘要+新消息重建。
func (s *Store) AppendTurnWithCompression(conversationId string, newMessages []model.Message,
	totalOutputTokens int, threshold int, compressor model.Compressor) error {

	if err := validateConversationId(conversationId); err != nil {
		return err
	}

	l := s.lockFor(conversationId)
	l.Lock()
	defer l.Unlock()

	now := time.Now()
	nowInstant := javaInstant(now)

	existing, err := s.readSessionJson(conversationId)
	if err != nil {
		return err
	}

	if existing == nil {
		title := firstUserText(newMessages)
		if title == "" {
			title = "未命名会话"
		}
		existing = &model.SessionJson{
			ConversationId:           conversationId,
			Title:                    title,
			RunningTotalTokens:       0,
			ContextWindowLimitTokens: 150000,
			CreatedAt:                nowInstant,
			Messages:                 []model.Message{},
		}
	}

	existing.LastInputTokens = 0
	existing.LastOutputTokens = totalOutputTokens
	existing.UpdatedAt = nowInstant

	newRunningTotal := existing.RunningTotalTokens + totalOutputTokens

	log.Printf("[Store] AppendTurnWithCompression: conv=%s, existingMsgs=%d, newMsgs=%d, oldTokens=%d, newTokens=%d, threshold=%d\n",
		conversationId, len(existing.Messages), len(newMessages), existing.RunningTotalTokens, totalOutputTokens, threshold)

	// handled 标记：压缩成功时为 true，跳过下面的普通追加逻辑
	handled := false

	// 压缩判断：超过阈值 AND 有旧消息 AND compressor 不为 nil
	if newRunningTotal > threshold && len(existing.Messages) > 0 && compressor != nil {
		summaryText, compressErr := compressor(existing.Messages)
		if compressErr != nil {
			// 压缩失败 → fallback 普通追加（不丢数据，和 Java 一致）
			log.Printf("[Store] 压缩失败，fallback 普通追加: %v\n", compressErr)
		} else if summaryText != "" {
			// 压缩成功：旧消息归档，会话重建
			s.archiveOldMessages(conversationId, existing.Messages)

			// 估算摘要 token 数（chars * 0.6 ≈ tokens，和 Java 一致）
			summaryTokens := len(summaryText) * 6 / 10

			// 重建会话：摘要消息 + 新消息
			summaryMsg := model.Message{
				Role: "assistant",
				Content: []model.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("[对话摘要] %s", summaryText)},
				},
			}
			existing.Messages = append([]model.Message{summaryMsg}, newMessages...)
			existing.RunningTotalTokens = summaryTokens + totalOutputTokens
			existing.Title = existing.Title + "（已压缩）"
			handled = true
		}
	}

	// 普通路径：压缩未触发、压缩失败、或无 compressor
	if !handled {
		existing.Messages = append(existing.Messages, newMessages...)
		existing.RunningTotalTokens = newRunningTotal
	}

	return s.writeSessionJson(existing)
}

// archiveOldMessages 将旧消息追加写入归档文件。
func (s *Store) archiveOldMessages(conversationId string, oldMessages []model.Message) {
	path := s.archivePath(conversationId)

	// 读已有归档（可能已经在之前压缩过）
	var archive []model.Message
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &archive)
	}
	if archive == nil {
		archive = []model.Message{}
	}

	archive = append(archive, oldMessages...)

	data, err := json.MarshalIndent(archive, "", "  ")
	if err != nil {
		log.Printf("[Store] 归档序列化失败: %v\n", err)
		return
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[Store] 归档写入失败: %v\n", err)
	}
}

// ========================================================================
// 辅助函数
// ========================================================================

// javaInstant 把 Go 的 time.Time 转为 Java Instant 格式的 float64。
// Java Instant 序列化为 epoch 秒 + 纳秒小数，例如 1781535974.995143100。
// time.Unix() 返回 epoch 秒，time.Nanosecond() 返回当前秒内的纳秒数。
func javaInstant(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// firstUserText 从消息列表中提取第一条用户文本，用于新会话标题。
// 标题截取前 40 字符。
func firstUserText(messages []model.Message) string {
	for _, msg := range messages {
		if msg.Role == "user" {
			for _, block := range msg.Content {
				if block.Type == "text" && block.Text != "" {
					text := strings.TrimSpace(block.Text)
					if len(text) > 40 {
						text = text[:40]
					}
					return text
				}
			}
		}
	}
	return ""
}
