package discordcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alice-bnuy/logutil"
)

// AvatarMultiGuildCache represents the cache file containing all guilds
type AvatarMultiGuildCache struct {
	Guilds      map[string]*AvatarCache `json:"guilds"`
	LastUpdated time.Time               `json:"last_updated"`
	Version     string                  `json:"version"`
}

type AvatarCacheManager struct {
	path       string
	configPath string
	guilds     map[string]*AvatarCache
	mu         sync.RWMutex
	saveMu     sync.Mutex
	lastSave   time.Time
}

func newAvatarCacheManager(configPath string) (*AvatarCacheManager, error) {
	if configPath == "" {
		return nil, fmt.Errorf("config path cannot be empty")
	}
	path := filepath.Join(configPath, "cache.json")
	return &AvatarCacheManager{
		path:       path,
		configPath: configPath,
		guilds:     make(map[string]*AvatarCache),
	}, nil
}

// Load loads the cache from the file.
func (m *AvatarCacheManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.guilds = make(map[string]*AvatarCache)
			logutil.WithField("path", m.path).Info("Cache file does not exist, initializing empty cache")
			return nil
		}
		logutil.WithFields(map[string]interface{}{
			"path":  m.path,
			"error": err,
		}).Error("Failed to read cache file")
		return fmt.Errorf(ErrReadCacheFile, err)
	}

	var multiCache AvatarMultiGuildCache
	if err := json.Unmarshal(data, &multiCache); err == nil && multiCache.Guilds != nil {
		m.guilds = multiCache.Guilds
		logutil.WithField("path", m.path).Info("Cache loaded successfully")
		return nil
	}

	var oldCache AvatarCache
	if err := json.Unmarshal(data, &oldCache); err != nil {
		logutil.WithFields(map[string]interface{}{
			"path":  m.path,
			"error": err,
		}).Error("Failed to unmarshal cache file")
		return fmt.Errorf(ErrUnmarshalCache, err)
	}

	m.guilds = make(map[string]*AvatarCache)
	if oldCache.GuildID != "" {
		m.guilds[oldCache.GuildID] = &oldCache
		logutil.WithField("guildID", oldCache.GuildID).Info("Loaded old cache format for guild")
	}

	return nil
}

// GuildCache retrieves or initializes the cache for a specific guild.
func (m *AvatarCacheManager) GuildCache(guildID string) *AvatarCache {
	m.mu.RLock()
	existing, ok := m.guilds[guildID]
	m.mu.RUnlock()
	if ok {
		logutil.WithField("guildID", guildID).Debug("Cache hit for guild")
		return existing
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cache, ok := m.guilds[guildID]; ok {
		logutil.WithField("guildID", guildID).Debug("Cache hit for guild after lock")
		return cache
	}
	cache := &AvatarCache{
		Avatars:     make(map[string]string),
		LastUpdated: time.Now(),
		GuildID:     guildID,
	}
	m.guilds[guildID] = cache
	logutil.WithField("guildID", guildID).Info("Initialized new cache for guild")
	return cache
}

// UpdateAvatar updates the avatar hash for a user in a specific guild.
func (m *AvatarCacheManager) UpdateAvatar(guildID, userID, avatarHash string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cache, exists := m.guilds[guildID]
	if !exists {
		cache = &AvatarCache{
			Avatars:     make(map[string]string),
			LastUpdated: time.Now(),
			GuildID:     guildID,
		}
		m.guilds[guildID] = cache
		logutil.WithField("guildID", guildID).Info("Created new cache for guild during avatar update")
	}

	cache.Avatars[userID] = avatarHash
	cache.LastUpdated = time.Now()
	logutil.WithFields(map[string]interface{}{
		"guildID":    guildID,
		"userID":     userID,
		"avatarHash": avatarHash,
	}).Info("Updated avatar in cache")
}

// AvatarHash retrieves the avatar hash for a user in a specific guild.
func (m *AvatarCacheManager) AvatarHash(guildID, userID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cache := m.guilds[guildID]; cache != nil {
		logutil.WithFields(map[string]interface{}{
			"guildID": guildID,
			"userID":  userID,
		}).Debug("Retrieved avatar hash from cache")
		return cache.Avatars[userID]
	}
	logutil.WithFields(map[string]interface{}{
		"guildID": guildID,
		"userID":  userID,
	}).Warn("Avatar hash not found in cache")
	return ""
}

// safeJoin ensures that the joined path is within the base directory.
func safeJoin(baseDir, relPath string) (string, error) {
	cleanBase := filepath.Clean(baseDir)
	cleanPath := filepath.Join(cleanBase, relPath)
	rel, err := filepath.Rel(cleanBase, cleanPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		logutil.WithFields(map[string]interface{}{
			"baseDir": baseDir,
			"relPath": relPath,
			"error":   err,
		}).Error("Invalid path detected")
		return "", errors.New(ErrInvalidPath)
	}
	return cleanPath, nil
}

// Save saves the avatar cache to the configured file path.
func (m *AvatarCacheManager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	projectRoot := "/Users/alice/Desktop/go/alice/development/alicemains"
	dir := filepath.Dir(m.path)
	safeDir, err := safeJoin(projectRoot, dir)
	if err != nil {
		logutil.WithFields(map[string]interface{}{
			"projectRoot": projectRoot,
			"dir":         dir,
			"error":       err,
		}).Error("Failed to resolve safe directory for saving cache")
		return err
	}
	if err := os.MkdirAll(safeDir, 0755); err != nil {
		logutil.WithFields(map[string]interface{}{
			"safeDir": safeDir,
			"error":   err,
		}).Error("Failed to create cache directory")
		return fmt.Errorf(ErrCreateCacheDir, err)
	}

	multiCache := AvatarMultiGuildCache{
		Guilds:      m.guilds,
		LastUpdated: time.Now(),
		Version:     "2.0",
	}

	data, err := json.Marshal(multiCache)
	if err != nil {
		logutil.WithFields(map[string]interface{}{
			"error": err,
		}).Error("Failed to marshal cache data")
		return fmt.Errorf(ErrMarshalCache, err)
	}

	if err := os.WriteFile(m.path, data, 0644); err != nil {
		logutil.WithFields(map[string]interface{}{
			"path":  m.path,
			"error": err,
		}).Error("Failed to write cache file")
		return fmt.Errorf(ErrWriteAvatarCache, err)
	}

	logutil.WithField("path", m.path).Info("Cache saved successfully")
	return nil
}

// SaveThrottled performs coalesced persistence respecting the minimum interval.
func (m *AvatarCacheManager) SaveThrottled(minInterval time.Duration) error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	if time.Since(m.lastSave) < minInterval {
		return nil
	}
	if err := m.Save(); err != nil {
		return err
	}
	m.lastSave = time.Now()
	return nil
}

// SaveForGuild saves only a specific guild (keeps compatibility)
func (m *AvatarCacheManager) SaveForGuild(guildID string) error {
	m.mu.Lock()
	if cache := m.guilds[guildID]; cache != nil {
		cache.LastUpdated = time.Now()
	}
	m.mu.Unlock()
	// Update guild-specific cache file path
	path, err := safeJoin(m.configPath, filepath.Join("cache", guildID+".json"))
	if err != nil {
		return err
	}
	cache := m.GuildCache(guildID)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf(ErrMarshalAvatarCache, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf(ErrCreateCacheDir, err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf(ErrWriteAvatarCache, err)
	}
	return nil
}

func (m *AvatarCacheManager) AvatarChanged(guildID, userID, currentAvatarHash string) bool {
	return m.AvatarHash(guildID, userID) != currentAvatarHash
}

// ClearForGuild removes the cache of a specific guild
func (m *AvatarCacheManager) ClearForGuild(guildID string) error {
	m.mu.Lock()
	if _, exists := m.guilds[guildID]; !exists {
		m.mu.Unlock()
		logutil.WithField("guildID", guildID).Warn(WarnNoGuildCache)
		return nil
	}
	delete(m.guilds, guildID)
	m.mu.Unlock()
	// Remove the guild's cache file safely
	projectRoot := "/Users/alice/Desktop/go/alice/development/alicemains"
	path, err := safeJoin(projectRoot, filepath.Join("cache", guildID+".json"))
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf(ErrRemoveAvatarCache, err)
	}
	return m.Save()
}

// GuildIDs returns a list of guilds that have cache
func (m *AvatarCacheManager) GuildIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.guilds))
	for guildID := range m.guilds {
		ids = append(ids, guildID)
	}
	return ids
}
