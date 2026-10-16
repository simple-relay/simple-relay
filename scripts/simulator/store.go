package main

import (
	"sync"
	"time"
)

// Event records a delivery forwarded by Simple Relay to the simulator.
type Event struct {
	ID                     string            `json:"id"`
	ReceivedAt             time.Time         `json:"received_at"`
	Method                 string            `json:"method"`
	Path                   string            `json:"path"`
	QueryParams            map[string]string `json:"query_params"`
	Headers                map[string]string `json:"headers"`
	Body                   string            `json:"body"`
	ResponseStatusReturned int               `json:"response_status_returned"`
	ResponseDelayMS        int               `json:"response_delay_ms"`
}

// MockConfig holds dynamic response configuration for the mock destination endpoint.
type MockConfig struct {
	StatusCode int               `json:"status_code"`
	DelayMS    int               `json:"delay_ms"`
	Headers    map[string]string `json:"headers,omitempty"`
}

// EventStore maintains in-memory received events and mock destination configuration.
type EventStore struct {
	mu         sync.RWMutex
	events     []*Event
	maxEvents  int
	mockConfig MockConfig
}

// NewEventStore creates a new in-memory EventStore.
func NewEventStore(maxEvents int) *EventStore {
	if maxEvents <= 0 {
		maxEvents = 100
	}
	return &EventStore{
		events:    make([]*Event, 0, maxEvents),
		maxEvents: maxEvents,
		mockConfig: MockConfig{
			StatusCode: 200,
			DelayMS:    0,
			Headers:    make(map[string]string),
		},
	}
}

// Add appends an event to the ring buffer, evicting the oldest when capacity is reached.
func (s *EventStore) Add(evt *Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.events) >= s.maxEvents {
		// Evict the oldest item
		s.events = s.events[1:]
	}
	s.events = append(s.events, evt)
}

// GetAll returns a copy of all buffered events (newest last).
func (s *EventStore) GetAll() []*Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Event, len(s.events))
	copy(result, s.events)
	return result
}

// Clear flushes all buffered events.
func (s *EventStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = make([]*Event, 0, s.maxEvents)
}

// Count returns the current number of buffered events.
func (s *EventStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

// GetMockConfig retrieves current mock configuration.
func (s *EventStore) GetMockConfig() MockConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfgCopy := s.mockConfig
	if cfgCopy.Headers != nil {
		headersCopy := make(map[string]string, len(cfgCopy.Headers))
		for k, v := range cfgCopy.Headers {
			headersCopy[k] = v
		}
		cfgCopy.Headers = headersCopy
	}
	return cfgCopy
}

// SetMockConfig updates mock destination configuration.
func (s *EventStore) SetMockConfig(cfg MockConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.StatusCode < 100 || cfg.StatusCode > 599 {
		cfg.StatusCode = 200
	}
	if cfg.DelayMS < 0 {
		cfg.DelayMS = 0
	}
	s.mockConfig = cfg
}
