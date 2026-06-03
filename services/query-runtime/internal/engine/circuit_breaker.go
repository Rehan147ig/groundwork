package engine

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit_open_fail_closed")

type CircuitBreaker struct {
	mu               sync.Mutex
	failureLimit     int
	openTimeout      time.Duration
	state            string
	consecutiveFails int
	openedAt         time.Time
}

func NewCircuitBreaker(failureLimit int, openTimeout time.Duration) *CircuitBreaker {
	if failureLimit <= 0 {
		failureLimit = 3
	}
	if openTimeout <= 0 {
		openTimeout = 10 * time.Second
	}
	return &CircuitBreaker{failureLimit: failureLimit, openTimeout: openTimeout, state: "closed"}
}

func (c *CircuitBreaker) Allow() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == "open" && time.Since(c.openedAt) >= c.openTimeout {
		c.state = "half_open"
	}
	if c.state == "open" {
		return ErrCircuitOpen
	}
	return nil
}

func (c *CircuitBreaker) ReportSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = "closed"
	c.consecutiveFails = 0
}

func (c *CircuitBreaker) ReportFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consecutiveFails++
	if c.state == "half_open" || c.consecutiveFails >= c.failureLimit {
		c.state = "open"
		c.openedAt = time.Now()
	}
}

func (c *CircuitBreaker) State() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}
