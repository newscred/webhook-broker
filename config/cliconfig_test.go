package config

import (
	"bytes"
	"errors"
	"io/ioutil"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func randomString() string {
	return strconv.Itoa(randomizer.Int())
}

var (
	wdTestPath                 = "../testdatadir/"
	randomizer                 = rand.New(rand.NewSource(time.Now().UnixNano()))
	notificationFilePath       = wdTestPath + "webhook-broker.change-test_" + randomString() + ".cfg"
	noChangeFilePath           = wdTestPath + "webhook-broker.no-notify_" + randomString() + ".cfg"
	removeFilePath             = wdTestPath + "webhook-broker.remove_" + randomString() + ".cfg"
	hashTestPath               = wdTestPath + "webhook-broker.hash-err_" + randomString() + ".cfg"
	truncateTestPath           = wdTestPath + "webhook-broker.truncate_" + randomString() + ".cfg"
	notificationInitialContent = `[broker]
	max-message-queue-size=10000000
	`
	notificationDifferentContent = `[broker]
	max-message-queue-size=1
	`
)

func writeToFile(filePath, content string) (err error) {
	err = ioutil.WriteFile(filePath, []byte(content), 0644)
	return err
}

func TestCLIConfigPathChangeNotification(t *testing.T) {
	t.Run("NotifiedOnFileChange", func(t *testing.T) {
		err := writeToFile(notificationFilePath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		cliConfig := &CLIConfig{ConfigPath: notificationFilePath}
		var wg sync.WaitGroup
		wg.Add(1)
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		defer cliConfig.StopWatcher()
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(5 * time.Millisecond)
		err = writeToFile(notificationFilePath, notificationDifferentContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		wg.Wait()
	})
	t.Run("NoNotifyOnFileContentUnchanged", func(t *testing.T) {
		err := writeToFile(noChangeFilePath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		cliConfig := &CLIConfig{ConfigPath: noChangeFilePath}
		var wg sync.WaitGroup
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		defer cliConfig.StopWatcher()
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(1 * time.Millisecond)
		err = writeToFile(noChangeFilePath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		time.Sleep(3 * time.Millisecond)
		wg.Wait()
	})
	t.Run("NoNotifyOnFileTruncation", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			log.Logger = oldLogger
		}()
		err := writeToFile(noChangeFilePath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		cliConfig := &CLIConfig{ConfigPath: noChangeFilePath}
		var wg sync.WaitGroup
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		defer cliConfig.StopWatcher()
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(1 * time.Millisecond)
		err = writeToFile(noChangeFilePath, "")
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		time.Sleep(3 * time.Millisecond)
		wg.Wait()
		assert.Contains(t, buf.String(), "truncation of config file not expected")
		assert.Contains(t, buf.String(), errTruncatedConfigFile.Error())
	})
	t.Run("NothingHappensOnFileRemoval", func(t *testing.T) {
		/*
			Nothing will happen when the file is removed and the worker will stop,
			this is primarily for code coverage nothing meaningful to assert other
			than the fact that if the file is created again subsequently then no
			change will be captured henceforth
		*/
		err := writeToFile(removeFilePath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		cliConfig := &CLIConfig{ConfigPath: removeFilePath}
		var wg sync.WaitGroup
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		defer cliConfig.StopWatcher()
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(1 * time.Millisecond)
		err = os.Remove(removeFilePath)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		time.Sleep(3 * time.Millisecond)
		wg.Wait()
	})
	t.Run("NoFilePathTest", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		oldDir, _ := os.Getwd()
		os.Chdir(wdTestPath)
		defer func() {
			os.Chdir(oldDir)
			log.Logger = oldLogger
		}()
		cliConfig := &CLIConfig{}
		var wg sync.WaitGroup
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		defer cliConfig.StopWatcher()
		time.Sleep(1 * time.Millisecond)
		assert.Contains(t, buf.String(), errNoFileToWatch.Error())
		assert.Contains(t, buf.String(), "could not find any file to watch")
	})
	t.Run("HashErrors", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			log.Logger = oldLogger
		}()
		expectedErr := errors.New("hash error from test")
		// Initial hash error
		oldGetHash := getFileHash
		errHashFn := func(filePath string) (string, error) {
			return "", expectedErr
		}
		defer func() { getFileHash = oldGetHash }()
		getFileHash = errHashFn
		err := writeToFile(hashTestPath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		cliConfig := &CLIConfig{ConfigPath: hashTestPath}
		var wg sync.WaitGroup
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		defer cliConfig.StopWatcher()
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(1 * time.Millisecond)
		assert.Contains(t, buf.String(), expectedErr.Error())
		assert.Contains(t, buf.String(), "could not generate original config file hash")
		getFileHash = oldGetHash
		cliConfig.watcherStarted = false
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(1 * time.Millisecond)
		getFileHash = errHashFn
		err = writeToFile(hashTestPath, notificationInitialContent)
		time.Sleep(1 * time.Millisecond)
		assert.Contains(t, buf.String(), expectedErr.Error())
		assert.Contains(t, buf.String(), "could not generate file hash on change")
	})
	t.Run("OpenFileErrorInGetHash", func(t *testing.T) {
		_, err := getFileHash(wdTestPath + ConfigFilename)
		assert.NotNil(t, err)
	})
	t.Run("OpenFileErrorInGetHashInline", func(t *testing.T) {
		oldCreateWatcher := createNewWatcher
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			createNewWatcher = oldCreateWatcher
			log.Logger = oldLogger
		}()
		expectedErr := errors.New("create watcher error from test")
		createNewWatcher = func() (*fsnotify.Watcher, error) {
			return nil, expectedErr
		}
		// Initial hash error
		err := writeToFile(hashTestPath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		cliConfig := &CLIConfig{ConfigPath: hashTestPath}
		var wg sync.WaitGroup
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(1 * time.Millisecond)
		assert.Contains(t, buf.String(), expectedErr.Error())
		assert.Contains(t, buf.String(), "could not setup watcher")
	})
	t.Run("PassErrorToWatcher", func(t *testing.T) {
		oldCreateWatcher := createNewWatcher
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() {
			createNewWatcher = oldCreateWatcher
			log.Logger = oldLogger
		}()
		expectedErr := errors.New("manual watch error from test")
		watcher, _ := fsnotify.NewWatcher()
		createNewWatcher = func() (*fsnotify.Watcher, error) {
			return watcher, nil
		}
		// Initial hash error
		err := writeToFile(hashTestPath, notificationInitialContent)
		if err != nil {
			log.Fatal().Err(err).Msg("could not write to file")
		}
		cliConfig := &CLIConfig{ConfigPath: hashTestPath}
		var wg sync.WaitGroup
		cliConfig.NotifyOnConfigFileChange(func() {
			wg.Done()
		})
		defer cliConfig.StopWatcher()
		assert.True(t, cliConfig.watcherStarted)
		time.Sleep(1 * time.Millisecond)
		watcher.Errors <- expectedErr
		time.Sleep(1 * time.Millisecond)
		assert.Contains(t, buf.String(), expectedErr.Error())
		assert.Contains(t, buf.String(), "watcher error")
	})
	t.Run("NoWatchDueToConfig", func(t *testing.T) {
		inConfig := &CLIConfig{DoNotWatchConfigChange: true}
		assert.True(t, inConfig.DoNotWatchConfigChange)
		inConfig.NotifyOnConfigFileChange(func() {
			t.FailNow()
		})
		assert.False(t, inConfig.IsConfigWatcherStarted())
	})
}
