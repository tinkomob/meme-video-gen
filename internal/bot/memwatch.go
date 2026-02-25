package bot

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

const (
	// memWarnThreshold: предупреждение при heap > 600 MB
	memWarnThresholdBytes = 600 * 1024 * 1024
	// memCritThreshold: экстренная остановка при heap > 1.2 GB
	memCritThresholdBytes = 1200 * 1024 * 1024
	// memCheckInterval: интервал проверки
	memCheckInterval = 30 * time.Second
	// goroutineWarnThreshold: предупреждение при goroutines > 500
	goroutineWarnThreshold = 500
	// goroutineCritThreshold: экстренная остановка при goroutines > 1000
	goroutineCritThreshold = 1000
)

// runMemoryWatcher следит за потреблением памяти и goroutine leak.
// При превышении порогов отправляет уведомление в Telegram.
// При критическом превышении — экстренно останавливает приложение через cancel().
func (b *TelegramBot) runMemoryWatcher(ctx context.Context) {
	ticker := time.NewTicker(memCheckInterval)
	defer ticker.Stop()

	var lastWarnAt time.Time

	b.log.Infof("memwatch: started (warn=%dMB, crit=%dMB, goroutines warn=%d crit=%d)",
		memWarnThresholdBytes/(1024*1024),
		memCritThresholdBytes/(1024*1024),
		goroutineWarnThreshold,
		goroutineCritThreshold,
	)

	for {
		select {
		case <-ctx.Done():
			b.log.Infof("memwatch: stopped")
			return
		case <-ticker.C:
			b.checkMemory(&lastWarnAt)
		}
	}
}

func (b *TelegramBot) checkMemory(lastWarnAt *time.Time) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	heapMB := ms.HeapAlloc / (1024 * 1024)
	sysMB := ms.Sys / (1024 * 1024)
	numGoroutines := runtime.NumGoroutine()

	// -- Goroutine leak check (critical) --
	if numGoroutines >= goroutineCritThreshold {
		msg := fmt.Sprintf(
			"🚨 УТЕЧКА ГОРУТИН — ЭКСТРЕННАЯ ОСТАНОВКА!\nGoroutines: %d (порог: %d)\nHeap: %d MB / Sys: %d MB",
			numGoroutines, goroutineCritThreshold, heapMB, sysMB,
		)
		b.log.Errorf("memwatch: CRITICAL goroutine leak — goroutines=%d", numGoroutines)
		b.sendMemAlert(msg, true)
		return
	}

	// -- Memory leak check (critical) --
	if ms.HeapAlloc >= memCritThresholdBytes {
		msg := fmt.Sprintf(
			"🚨 КРИТИЧЕСКАЯ УТЕЧКА ПАМЯТИ — ЭКСТРЕННАЯ ОСТАНОВКА!\nHeap: %d MB (порог: %d MB)\nSys: %d MB\nGoroutines: %d",
			heapMB, memCritThresholdBytes/(1024*1024), sysMB, numGoroutines,
		)
		b.log.Errorf("memwatch: CRITICAL heap leak — heap=%dMB goroutines=%d", heapMB, numGoroutines)
		b.sendMemAlert(msg, true)
		return
	}

	// -- Warning thresholds --
	warnNeeded := ms.HeapAlloc > memWarnThresholdBytes || numGoroutines >= goroutineWarnThreshold
	if warnNeeded && time.Since(*lastWarnAt) > 10*time.Minute {
		msg := fmt.Sprintf(
			"⚠️ Высокое потребление ресурсов!\nHeap: %d MB (порог: %d MB)\nSys: %d MB\nGoroutines: %d (порог: %d)\n\nМониторинг продолжается.",
			heapMB, memWarnThresholdBytes/(1024*1024), sysMB,
			numGoroutines, goroutineWarnThreshold,
		)
		b.log.Warnf("memwatch: WARNING heap=%dMB goroutines=%d", heapMB, numGoroutines)
		b.sendMemAlert(msg, false)
		runtime.GC()
		*lastWarnAt = time.Now()
	}
}

// sendMemAlert отправляет уведомление о утечке в admin-чат.
// Если emergency=true — вызывает cancelFunc для экстренной остановки приложения.
func (b *TelegramBot) sendMemAlert(msg string, emergency bool) {
	chatID := b.svc.GetConfig().PostsChatID
	if chatID != 0 {
		b.replyText(chatID, msg)
		if emergency {
			// Ждём 3 сек для доставки сообщения перед остановкой
			time.Sleep(3 * time.Second)
		}
	} else {
		b.log.Warnf("memwatch: PostsChatID=0, alert not delivered via Telegram: %s", msg)
	}

	if emergency && b.cancelFunc != nil {
		b.log.Errorf("memwatch: calling cancelFunc to initiate emergency shutdown")
		b.cancelFunc()
	}
}
