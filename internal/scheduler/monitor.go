package scheduler

import (
	"context"
	"runtime"
	"sync"
	"time"

	"meme-video-gen/internal/logging"
)

// ResourceMonitor continuously monitors and ensures required counts for songs, sources, and memes
type ResourceMonitor struct {
	svc          *Service
	log          *logging.Logger
	stopCh       chan struct{}
	wg           sync.WaitGroup
	checkTicker  *time.Ticker
	parallelMode bool // true = parallel, false = concurrent

	// Synchronization channels for parallel mode
	sourcesReadyCh chan struct{}
	sourcesOnce    sync.Once
}

func NewResourceMonitor(svc *Service, log *logging.Logger) *ResourceMonitor {
	// Use concurrent mode (sequential) to avoid deadlocks and hangs
	parallelMode := false

	log.Infof("resource monitor: CPU cores=%d, mode=%s", runtime.NumCPU(),
		map[bool]string{true: "parallel", false: "concurrent"}[parallelMode])

	return &ResourceMonitor{
		svc:            svc,
		log:            log,
		stopCh:         make(chan struct{}),
		checkTicker:    time.NewTicker(5 * time.Minute), // Check every 5 minutes
		parallelMode:   parallelMode,
		sourcesReadyCh: make(chan struct{}),
	}
}

// Start begins monitoring resources
func (m *ResourceMonitor) Start(ctx context.Context) {
	m.log.Infof("resource monitor: starting...")

	// Initial sync and check
	go func() {
		m.wg.Add(1)
		defer m.wg.Done()
		m.initialSync(ctx)
	}()

	// Periodic checks
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.monitorLoop(ctx)
	}()
}

// Stop gracefully stops the monitor
func (m *ResourceMonitor) Stop() {
	m.log.Infof("resource monitor: stopping...")
	close(m.stopCh)
	m.checkTicker.Stop()
	m.wg.Wait()
	m.log.Infof("resource monitor: stopped")
}

// initialSync performs initial synchronization and validation
func (m *ResourceMonitor) initialSync(ctx context.Context) {
	m.log.Infof("resource monitor: initial sync started")

	// Step 1: Sync all JSON files with S3 folders
	m.log.Infof("resource monitor: syncing sources with S3")
	if err := m.svc.SyncSources(ctx); err != nil {
		m.log.Errorf("resource monitor: source sync failed: %v", err)
	}

	m.log.Infof("resource monitor: syncing memes with S3")
	if err := m.svc.SyncMemes(ctx); err != nil {
		m.log.Errorf("resource monitor: meme sync failed: %v", err)
	}

	// Step 2: Check and ensure resources immediately after sync
	m.log.Infof("resource monitor: checking resource counts")
	m.ensureResources(ctx)

	m.log.Infof("resource monitor: initial sync completed")
}

// monitorLoop continuously monitors and ensures resource counts
func (m *ResourceMonitor) monitorLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-m.checkTicker.C:
			m.log.Infof("resource monitor: periodic check triggered")

			// Check if we need to switch to aggressive mode (faster checks)
			memesCount, _ := m.svc.GetMemesCount(ctx)
			sourcesCount, _ := m.svc.GetSourcesCount(ctx)
			cfg := m.svc.GetConfig()

			// If resources are critically low, check more frequently
			needsAggressiveMode := memesCount < cfg.MaxMemes/2 || sourcesCount < cfg.MaxSources/2

			if needsAggressiveMode && m.checkTicker.C != nil {
				m.log.Infof("resource monitor: switching to aggressive mode (checking every 1 minute)")
				m.checkTicker.Reset(1 * time.Minute)
			} else if !needsAggressiveMode && m.checkTicker.C != nil {
				// Back to normal mode
				m.checkTicker.Reset(5 * time.Minute)
			}

			m.ensureResources(ctx)
		}
	}
}

// ensureResources checks and ensures all resources meet required counts
func (m *ResourceMonitor) ensureResources(ctx context.Context) {
	// Get current counts
	songsCount, err := m.svc.GetSongsCount(ctx)
	if err != nil {
		m.log.Errorf("resource monitor: failed to get songs count: %v", err)
		songsCount = 0
	}

	sourcesCount, err := m.svc.GetSourcesCount(ctx)
	if err != nil {
		m.log.Errorf("resource monitor: failed to get sources count: %v", err)
		sourcesCount = 0
	}

	memesCount, err := m.svc.GetMemesCount(ctx)
	if err != nil {
		m.log.Errorf("resource monitor: failed to get memes count: %v", err)
		memesCount = 0
	}

	cfg := m.svc.GetConfig()

	m.log.Infof("resource monitor: current counts - songs: %d, sources: %d/%d, memes: %d/%d",
		songsCount, sourcesCount, cfg.MaxSources, memesCount, cfg.MaxMemes)

	// Determine what needs to be done
	needSongs := songsCount == 0
	needSources := sourcesCount < cfg.MaxSources
	needMemes := memesCount < cfg.MaxMemes

	if !needSongs && !needSources && !needMemes {
		m.log.Infof("resource monitor: all resources are sufficient")
		return
	}

	// Reset sourcesReadyCh for this run (important for periodic checks)
	m.sourcesReadyCh = make(chan struct{})
	m.sourcesOnce = sync.Once{}

	// Execute tasks based on mode
	if m.parallelMode {
		m.ensureResourcesParallel(ctx, needSongs, needSources, needMemes)
	} else {
		m.ensureResourcesConcurrent(ctx, needSongs, needSources, needMemes)
	}
}

// ensureResourcesParallel runs all ensure tasks in parallel (multi-core)
func (m *ResourceMonitor) ensureResourcesParallel(ctx context.Context, needSongs, needSources, needMemes bool) {
	m.log.Infof("resource monitor: ensuring resources in PARALLEL mode")

	var wg sync.WaitGroup

	// Songs task (independent, runs in parallel)
	if needSongs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.log.Infof("resource monitor: [parallel] ensuring songs...")
			if err := m.svc.Impl().EnsureSongs(ctx); err != nil {
				m.log.Errorf("resource monitor: [parallel] ensure songs failed: %v", err)
			} else {
				m.log.Infof("resource monitor: [parallel] songs ensured")
			}
		}()
	}

	// Sources task (runs in parallel, signals when ready)
	if needSources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.log.Infof("resource monitor: [parallel] ensuring sources...")
			if err := m.svc.Impl().EnsureSources(ctx); err != nil {
				m.log.Errorf("resource monitor: [parallel] ensure sources failed: %v", err)
			} else {
				m.log.Infof("resource monitor: [parallel] sources ensured")
			}
			// Signal that sources are ready
			m.sourcesOnce.Do(func() {
				close(m.sourcesReadyCh)
			})
		}()
	} else {
		// If sources are already sufficient, signal immediately
		m.sourcesOnce.Do(func() {
			close(m.sourcesReadyCh)
		})
	}

	// Memes task - waits for sources to be ready before starting
	if needMemes {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Wait for sources to be ready
			m.log.Infof("resource monitor: [parallel] waiting for sources to be ready before generating memes...")
			select {
			case <-m.sourcesReadyCh:
				m.log.Infof("resource monitor: [parallel] sources ready, starting meme generation")
			case <-ctx.Done():
				m.log.Warnf("resource monitor: [parallel] context cancelled while waiting for sources")
				return
			case <-time.After(2 * time.Minute):
				m.log.Warnf("resource monitor: [parallel] timeout waiting for sources, starting meme generation anyway")
			}

			m.log.Infof("resource monitor: [parallel] ensuring memes...")
			if err := m.svc.Impl().EnsureMemes(ctx); err != nil {
				m.log.Errorf("resource monitor: [parallel] ensure memes failed: %v", err)
			} else {
				m.log.Infof("resource monitor: [parallel] memes ensured")
			}
		}()
	}

	wg.Wait()
	m.log.Infof("resource monitor: parallel resource ensuring completed")
}

// ensureResourcesConcurrent runs tasks one after another (single-core)
func (m *ResourceMonitor) ensureResourcesConcurrent(ctx context.Context, needSongs, needSources, needMemes bool) {
	m.log.Infof("resource monitor: ensuring resources in CONCURRENT mode")

	if needSongs {
		m.log.Infof("resource monitor: [concurrent] ensuring songs...")
		if err := m.svc.Impl().EnsureSongs(ctx); err != nil {
			m.log.Errorf("resource monitor: [concurrent] ensure songs failed: %v", err)
		} else {
			m.log.Infof("resource monitor: [concurrent] songs ensured")
		}
	}

	if needSources {
		m.log.Infof("resource monitor: [concurrent] ensuring sources...")
		if err := m.svc.Impl().EnsureSources(ctx); err != nil {
			m.log.Errorf("resource monitor: [concurrent] ensure sources failed: %v", err)
		} else {
			m.log.Infof("resource monitor: [concurrent] sources ensured")
		}
	}

	if needMemes {
		m.log.Infof("resource monitor: [concurrent] ensuring memes...")
		if err := m.svc.Impl().EnsureMemes(ctx); err != nil {
			m.log.Errorf("resource monitor: [concurrent] ensure memes failed: %v", err)
		} else {
			m.log.Infof("resource monitor: [concurrent] memes ensured")
		}
	}

	m.log.Infof("resource monitor: concurrent resource ensuring completed")
}

// ForceCheck triggers an immediate resource check (useful for manual triggers)
func (m *ResourceMonitor) ForceCheck(ctx context.Context) {
	m.log.Infof("resource monitor: force check triggered")
	m.ensureResources(ctx)
}
