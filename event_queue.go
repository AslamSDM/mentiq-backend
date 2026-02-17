package main

import (
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
)

// EventQueue buffers incoming events in a Go channel and batch-inserts them
// into PostgreSQL/TimescaleDB at regular intervals or when the buffer fills up.
//
// This decouples HTTP request latency from database write latency:
// - HTTP handler returns immediately after enqueueing
// - Background workers drain the queue in batches
// - If the queue is full, Enqueue blocks (backpressure to the caller)
type EventQueue struct {
	ch       chan Event
	db       *gorm.DB
	wg       sync.WaitGroup
	stopChan chan struct{}

	// Configuration
	batchSize    int           // max events per INSERT
	flushTimeout time.Duration // max time between flushes
	workers      int           // number of concurrent flush workers
}

// EventQueueConfig holds configuration for the event queue.
type EventQueueConfig struct {
	// QueueSize is the channel buffer size. Backpressure kicks in when full.
	// Default: 10000
	QueueSize int

	// BatchSize is the max number of events per batch INSERT.
	// Default: 500
	BatchSize int

	// FlushTimeout is the max duration between flushes.
	// Default: 2 seconds
	FlushTimeout time.Duration

	// Workers is the number of concurrent goroutines draining the queue.
	// Default: 2
	Workers int
}

// DefaultEventQueueConfig returns sensible defaults for a single-server deployment.
func DefaultEventQueueConfig() EventQueueConfig {
	return EventQueueConfig{
		QueueSize:    10_000,
		BatchSize:    500,
		FlushTimeout: 2 * time.Second,
		Workers:      2,
	}
}

// NewEventQueue creates and starts an event queue with the given config.
func NewEventQueue(db *gorm.DB, cfg EventQueueConfig) *EventQueue {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 10_000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}
	if cfg.FlushTimeout <= 0 {
		cfg.FlushTimeout = 2 * time.Second
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}

	eq := &EventQueue{
		ch:           make(chan Event, cfg.QueueSize),
		db:           db,
		stopChan:     make(chan struct{}),
		batchSize:    cfg.BatchSize,
		flushTimeout: cfg.FlushTimeout,
		workers:      cfg.Workers,
	}

	// Start worker goroutines
	for i := 0; i < eq.workers; i++ {
		eq.wg.Add(1)
		go eq.worker(i)
	}

	log.Printf("Event queue started: queueSize=%d, batchSize=%d, flushTimeout=%s, workers=%d",
		cfg.QueueSize, cfg.BatchSize, cfg.FlushTimeout, cfg.Workers)

	return eq
}

// Enqueue adds an event to the queue. Returns false if the queue is full.
func (eq *EventQueue) Enqueue(event Event) bool {
	select {
	case eq.ch <- event:
		return true
	default:
		// Queue is full — apply backpressure
		return false
	}
}

// EnqueueBatch adds multiple events to the queue. Returns the count of successfully enqueued events.
func (eq *EventQueue) EnqueueBatch(events []Event) int {
	enqueued := 0
	for i := range events {
		if eq.Enqueue(events[i]) {
			enqueued++
		} else {
			break // Queue full, stop trying
		}
	}
	return enqueued
}

// Len returns the current number of events waiting in the queue.
func (eq *EventQueue) Len() int {
	return len(eq.ch)
}

// Stop gracefully shuts down the queue: stops accepting new events,
// flushes remaining events, and waits for workers to finish.
func (eq *EventQueue) Stop() {
	log.Println("Event queue shutting down, flushing remaining events...")
	close(eq.stopChan)
	eq.wg.Wait()

	// Drain any remaining events after workers stop
	remaining := eq.drainAll()
	if len(remaining) > 0 {
		eq.flushBatch(remaining)
	}

	log.Println("Event queue shutdown complete")
}

// worker is a goroutine that collects events from the channel and flushes them in batches.
func (eq *EventQueue) worker(id int) {
	defer eq.wg.Done()

	batch := make([]Event, 0, eq.batchSize)
	timer := time.NewTimer(eq.flushTimeout)
	defer timer.Stop()

	for {
		select {
		case event, ok := <-eq.ch:
			if !ok {
				// Channel closed — flush remaining
				if len(batch) > 0 {
					eq.flushBatch(batch)
				}
				return
			}
			batch = append(batch, event)
			if len(batch) >= eq.batchSize {
				eq.flushBatch(batch)
				batch = batch[:0]
				timer.Reset(eq.flushTimeout)
			}

		case <-timer.C:
			if len(batch) > 0 {
				eq.flushBatch(batch)
				batch = batch[:0]
			}
			timer.Reset(eq.flushTimeout)

		case <-eq.stopChan:
			// Flush what we have and exit
			if len(batch) > 0 {
				eq.flushBatch(batch)
			}
			return
		}
	}
}

// flushBatch writes a batch of events to the database.
func (eq *EventQueue) flushBatch(batch []Event) {
	if len(batch) == 0 {
		return
	}

	start := time.Now()
	result := eq.db.Create(&batch)
	duration := time.Since(start)

	if result.Error != nil {
		log.Printf("Event queue: failed to flush %d events (%v): %v", len(batch), duration, result.Error)
		// Retry once after a short delay
		time.Sleep(500 * time.Millisecond)
		retryResult := eq.db.Create(&batch)
		if retryResult.Error != nil {
			log.Printf("Event queue: retry also failed for %d events: %v", len(batch), retryResult.Error)
			// Events are lost at this point. In production you'd want a dead-letter file.
		} else {
			log.Printf("Event queue: retry succeeded, flushed %d events", len(batch))
		}
		return
	}

	log.Printf("Event queue: flushed %d events in %v", len(batch), duration)
}

// drainAll pulls all remaining events from the channel without blocking.
func (eq *EventQueue) drainAll() []Event {
	var events []Event
	for {
		select {
		case event, ok := <-eq.ch:
			if !ok {
				return events
			}
			events = append(events, event)
		default:
			return events
		}
	}
}
