package runtime

import (
	"sync"
	"time"
)

type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

type CircuitBreakerSettings struct {
	Name          string
	FailureLimit  int
	OpenTimeout   time.Duration
	HalfOpenLimit int
}

type CircuitBreaker struct {
	mu               sync.Mutex
	settings         CircuitBreakerSettings
	state            CircuitState
	consecutiveFails int
	openedAt         time.Time
	halfOpenInFlight int
}

func NewCircuitBreaker(settings CircuitBreakerSettings) *CircuitBreaker {
	if settings.FailureLimit <= 0 {
		settings.FailureLimit = 3
	}
	if settings.OpenTimeout <= 0 {
		settings.OpenTimeout = 10 * time.Second
	}
	if settings.HalfOpenLimit <= 0 {
		settings.HalfOpenLimit = 1
	}
	return &CircuitBreaker{settings: settings, state: CircuitClosed}
}

func (c *CircuitBreaker) Allow() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == CircuitOpen && time.Since(c.openedAt) >= c.settings.OpenTimeout {
		c.state = CircuitHalfOpen
		c.halfOpenInFlight = 0
	}

	switch c.state {
	case CircuitOpen:
		return ErrCircuitOpen
	case CircuitHalfOpen:
		if c.halfOpenInFlight >= c.settings.HalfOpenLimit {
			return ErrCircuitOpen
		}
		c.halfOpenInFlight++
	}
	return nil
}

func (c *CircuitBreaker) ReportSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = CircuitClosed
	c.consecutiveFails = 0
	c.halfOpenInFlight = 0
}

func (c *CircuitBreaker) ReportFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == CircuitHalfOpen {
		c.open()
		return
	}
	c.consecutiveFails++
	if c.consecutiveFails >= c.settings.FailureLimit {
		c.open()
	}
}

func (c *CircuitBreaker) State() CircuitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *CircuitBreaker) open() {
	c.state = CircuitOpen
	c.openedAt = time.Now()
	c.halfOpenInFlight = 0
}
