package discordcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

type CacheManager struct {
	path       string
	configPath string
	guilds     map[string]*AvatarCache
	mu         sync.RWMutex
	saveMu     sync.Mutex
	lastSave   time.Time
}

func newCacheManager(configPath string) (*CacheManager, error) {
	if configPath == "" {
		return nil, fmt.Errorf("config path cannot be empty")
	}
	path := filepath.Join(configPath, "cache.json")
	return &CacheManager{
		path:       path,
		configPath: configPath,
		guilds:     make(map[string]*AvatarCache),
	}, nil
}

// Load loads the cache from the file.
func (m *CacheManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.guilds = make(map[string]*AvatarCache)
			defaultCache := `{"guilds":{},"last_updated":"","version":"1.0"}`
			if err := os.WriteFile(m.path, []byte(defaultCache), 0666); err != nil {
				logutil.Errorf("Failed to write cache file at %s: %v", m.path, err)
				return fmt.Errorf("failed to create cache file: %w", err)
			}
			logutil.Infof("Cache file successfully written at %s", m.path)
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
func (m *CacheManager) GuildCache(guildID string) *AvatarCache {
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
func (m *CacheManager) UpdateAvatar(guildID, userID, avatarHash string) {
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
func (m *CacheManager) AvatarHash(guildID, userID string) string {
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

// Save saves the avatar cache to the configured file path.
func (m *CacheManager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Ensure cache directory exists
	cacheDir := filepath.Dir(m.path)
	if err := ensureDirectories(cacheDir); err != nil {
		logutil.WithFields(map[string]interface{}{
			"cacheDir": cacheDir,
			"error":    err,
		}).Error("Failed to create cache directory")
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	data, err := json.MarshalIndent(&AvatarMultiGuildCache{
		Guilds:      m.guilds,
		LastUpdated: time.Now(),
		Version:     "1.0",
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	if err := os.WriteFile(m.path, data, 0644); err != nil {
		logutil.WithFields(map[string]interface{}{
			"path":  m.path,
			"error": err,
		}).Error("Failed to write cache file")
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	logutil.WithField("path", m.path).Debug("Cache saved successfully")
	return nil
}

// SaveThrottled performs coalesced persistence respecting the minimum interval.
func (m *CacheManager) SaveThrottled(minInterval time.Duration) error {
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
func (m *CacheManager) SaveForGuild(guildID string) error {
	m.mu.Lock()
	if cache := m.guilds[guildID]; cache != nil {
		cache.LastUpdated = time.Now()
	}
	m.mu.Unlock()

	// Update guild-specific cache file path
	guildCachePath := filepath.Join(m.configPath, "cache", guildID+".json")
	cache := m.GuildCache(guildID)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf(ErrMarshalAvatarCache, err)
	}
	if err := createDirectory(filepath.Dir(guildCachePath)); err != nil {
		return fmt.Errorf(ErrCreateCacheDir, err)
	}
	if err := os.WriteFile(guildCachePath, data, 0666); err != nil {
		return fmt.Errorf(ErrWriteAvatarCache, err)
	}
	return nil
}

func (m *CacheManager) AvatarChanged(guildID, userID, currentAvatarHash string) bool {
	return m.AvatarHash(guildID, userID) != currentAvatarHash
}

// ClearForGuild removes the cache of a specific guild
func (m *CacheManager) ClearForGuild(guildID string) error {
	m.mu.Lock()
	if _, exists := m.guilds[guildID]; !exists {
		m.mu.Unlock()
		logutil.WithField("guildID", guildID).Warn(WarnNoGuildCache)
		return nil
	}
	delete(m.guilds, guildID)
	m.mu.Unlock()

	// Remove the guild's cache file
	guildCachePath := filepath.Join(m.configPath, "cache", guildID+".json")
	if err := os.Remove(guildCachePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove guild cache file: %w", err)
	}

	return m.Save()
}

// GuildIDs returns a list of guilds that have cache
func (m *CacheManager) GuildIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.guilds))
	for guildID := range m.guilds {
		ids = append(ids, guildID)
	}
	return ids
}
