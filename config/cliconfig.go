package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/fsnotify/fsnotify"
)

var (
	errNoFileToWatch       = errors.New("no file to watch")
	errTruncatedConfigFile = errors.New("truncated config file")
)

// CLIConfig represents the Command Line Args config
type CLIConfig struct {
	ConfigPath             string
	MigrationSource        string
	StopOnConfigChange     bool
	DoNotWatchConfigChange bool
	callbacks              []func()
	watcherStarted         bool
	watcherStarterMutex    sync.Mutex
	watcher                *fsnotify.Watcher
}

// IsMigrationEnabled returns whether migration is enabled
func (conf *CLIConfig) IsMigrationEnabled() bool {
	return len(conf.MigrationSource) > 0
}

// NotifyOnConfigFileChange registers a callback function for changes to ConfigPath; it calls the `callback` when a change is detected
func (conf *CLIConfig) NotifyOnConfigFileChange(callback func()) {
	if conf.DoNotWatchConfigChange {
		return
	}
	conf.callbacks = append(conf.callbacks, callback)
	if !conf.watcherStarted {
		conf.startConfigWatcher()
	}
}

// IsConfigWatcherStarted returns whether config watcher is running
func (conf *CLIConfig) IsConfigWatcherStarted() bool {
	return conf.watcherStarted
}

func (conf *CLIConfig) startConfigWatcher() {
	conf.watcherStarterMutex.Lock()
	defer conf.watcherStarterMutex.Unlock()
	conf.watchFileIfExists()
	conf.watcherStarted = true
}

// StopWatcher stops any watcher if started for CLI ConfigPath file change
func (conf *CLIConfig) StopWatcher() {
	if conf.watcherStarted {
		log.Print("closing watcher")
		conf.watcher.Close()
	}
}

type watcherWorkerConfig struct {
	configFile     string
	filename       string
	realConfigFile string
	filehash       string
	callbacks      []func()
}

func (conf *CLIConfig) watchFileIfExists() {
	watcher, err := createNewWatcher()
	if err != nil {
		log.Error().Err(err).Msg("could not setup watcher")
		return
	}
	conf.watcher = watcher
	// we have to watch the entire directory to pick up renames/atomic saves in a cross-platform way
	filename, err := getFileToWatch(conf.ConfigPath)
	if err != nil {
		log.Error().Err(err).Msg("could not get file to watch")
		return
	}
	configFile := filepath.Clean(filename)
	configDir, _ := filepath.Split(configFile)
	realConfigFile, _ := filepath.EvalSymlinks(filename)
	filehash, err := getFileHash(realConfigFile)
	if err != nil {
		log.Error().Err(err).Msg("could not generate original config file hash")
		return
	}
	watcherConfig := &watcherWorkerConfig{filename: filename, configFile: configFile, realConfigFile: realConfigFile, filehash: filehash, callbacks: conf.callbacks}
	watcher.Add(configDir)
	go watchWorker(watcher, watcherConfig)
}

func watchWorker(watcher *fsnotify.Watcher, workerConf *watcherWorkerConfig) {
	// Heavily inspired from - https://github.com/spf13/viper/blob/8c894384998e656900b125e674b8c20dbf87cc06/viper.go
	for {
		select {
		case event, ok := <-watcher.Events:
			if ok {
				if processFileChangeEvent(&event, workerConf) {
					return
				}
			}
		case err, ok := <-watcher.Errors:
			if ok {
				log.Warn().Err(err).Msg("watcher error")
			}
			return
		}
	}
}

var (
	processFileChangeEvent = func(event *fsnotify.Event, workerConf *watcherWorkerConfig) bool {
		currentConfigFile, _ := filepath.EvalSymlinks(workerConf.filename)
		const writeOrCreateMask = fsnotify.Write | fsnotify.Create
		log.Debug().Uint32("writeOrCreateMask", uint32(event.Op)).Str("eventName", event.Name).Msg("File change event")
		if (filepath.Clean(event.Name) == workerConf.configFile &&
			event.Op&writeOrCreateMask != 0) ||
			(currentConfigFile != "" && currentConfigFile != workerConf.realConfigFile) {
			workerConf.realConfigFile = currentConfigFile
			workerConf.filehash = callCallbacksIfChanged(workerConf.realConfigFile, workerConf.filehash, workerConf.callbacks)

		} else if filepath.Clean(event.Name) == workerConf.configFile &&
			event.Op&fsnotify.Remove&fsnotify.Remove != 0 {
			return true
		}
		return false
	}

	callCallbacksIfChanged = func(realConfigFile, oldHash string, callbacks []func()) string {
		newhash, err := getFileHash(realConfigFile)
		if err != nil {
			if err == errTruncatedConfigFile {
				log.Warn().Err(err).Msg("truncation of config file not expected")
			} else {
				log.Error().Err(err).Msg("could not generate file hash on change")
			}
			return oldHash
		}
		log.Debug().Str("oldHash", oldHash).Str("newHash", newhash).Msg("Old and new hash")
		if newhash != oldHash {
			for _, callback := range callbacks {
				go callback()
			}
		}
		return newhash
	}

	createNewWatcher = func() (*fsnotify.Watcher, error) {
		return fsnotify.NewWatcher()
	}

	getFileToWatch = func(configPath string) (filename string, err error) {
		filename = configPath
		fileInfo, err := os.Stat(filename)
		if err != nil || !fileInfo.Mode().IsRegular() {
			filename = ConfigFilename
			fileInfo, err = os.Stat(filename)
			if err != nil || !fileInfo.Mode().IsRegular() {
				log.Warn().Err(errNoFileToWatch).Msg("could not find any file to watch")
				return "", errNoFileToWatch
			}
		}
		return filename, nil
	}

	getFileHash = func(filePath string) (hashHex string, err error) {
		file, err := os.Open(filePath)
		if err != nil {
			return "", err
		}
		defer file.Close()

		var buf bytes.Buffer
		if _, err = io.Copy(&buf, file); err == nil {
			log.Debug().Str("Content", buf.String()).Msg("Content generating hash for")
			if buf.Len() == 0 {
				return "", errTruncatedConfigFile
			}
			hasher := sha256.New()
			if _, err = io.Copy(hasher, strings.NewReader(buf.String())); err == nil {
				hashHex = hex.EncodeToString(hasher.Sum(nil))
			}
		}
		return hashHex, err
	}
)
