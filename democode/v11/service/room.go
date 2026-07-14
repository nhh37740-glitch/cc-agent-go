package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cc-agent-go/democode/v11/model"
)

type Player struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Personality string `json:"personality"`
	Avatar      string `json:"avatar"`
	Alive       bool   `json:"alive"`
}

type RoomMessage struct {
	From      string               `json:"from"`
	Role      string               `json:"role"`
	Content   []model.ContentBlock `json:"content"`
	VisibleTo []string             `json:"visibleTo,omitempty"`
	Timestamp float64              `json:"timestamp"`
}

type RoomJson struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	GameType  string        `json:"gameType"`
	Status    string        `json:"status"`
	Messages  []RoomMessage `json:"messages"`
	CreatedAt float64       `json:"createdAt"`
	UpdatedAt float64       `json:"updatedAt"`
}

type RoomManager struct {
	dir   string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewRoomManager(dir string) *RoomManager {
	os.MkdirAll(dir, 0755)
	return &RoomManager{dir: dir, locks: make(map[string]*sync.Mutex)}
}

var safeRoomId = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

func (m *RoomManager) CreateRoom(id, name, gameType string, players []Player) (*RoomJson, error) {
	if !safeRoomId.MatchString(id) {
		return nil, fmt.Errorf("无效 roomId")
	}
	l := m.lockFor(id)
	l.Lock()
	defer l.Unlock()
	now := float64(time.Now().UnixMilli()) / 1000
	room := &RoomJson{ID: id, Name: name, GameType: gameType, Status: "playing", Messages: []RoomMessage{}, CreatedAt: now, UpdatedAt: now}
	return room, m.write(room)
}

func (m *RoomManager) LoadRoom(roomID string) (*RoomJson, error) {
	data, err := os.ReadFile(m.path(roomID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var room RoomJson
	if err := json.Unmarshal(data, &room); err != nil {
		return nil, err
	}
	return &room, nil
}

func (m *RoomManager) AppendMessage(roomID string, msg RoomMessage) error {
	l := m.lockFor(roomID)
	l.Lock()
	defer l.Unlock()
	room, _ := m.load(roomID)
	if room == nil {
		return fmt.Errorf("房间不存在: %s", roomID)
	}
	msg.Timestamp = float64(time.Now().UnixMilli()) / 1000
	room.Messages = append(room.Messages, msg)
	room.UpdatedAt = msg.Timestamp
	return m.write(room)
}

func (m *RoomManager) ListRooms() ([]string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *RoomManager) DeleteRoom(roomID string) error {
	return os.Remove(m.path(roomID))
}

func (m *RoomManager) lockFor(roomID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.locks[roomID]; ok {
		return l
	}
	l := &sync.Mutex{}
	m.locks[roomID] = l
	return l
}

func (m *RoomManager) path(roomID string) string { return filepath.Join(m.dir, roomID+".json") }

func (m *RoomManager) load(roomID string) (*RoomJson, error) {
	data, err := os.ReadFile(m.path(roomID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取房间失败: %w", err)
	}
	var room RoomJson
	if err := json.Unmarshal(data, &room); err != nil {
		return nil, fmt.Errorf("解析房间失败: %w", err)
	}
	return &room, nil
}

func (m *RoomManager) write(room *RoomJson) error {
	data, err := json.MarshalIndent(room, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(m.path(room.ID), data, 0644)
}
