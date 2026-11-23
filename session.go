package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"outline-reader/outline"
	"outline-reader/storage"
)

var errLocked = errors.New("хранилище не инициализировано")

const retireTimeout = 15 * time.Second

type session struct {
	db     *storage.DB
	client *outline.Client
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (s *session) done() { s.wg.Done() }

func (a *App) attachSession(db *storage.DB) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{db: db, ctx: ctx, cancel: cancel}
	a.mu.Lock()
	a.sess = s
	a.keyLost = false
	a.mu.Unlock()
	return s
}

func (a *App) acquire() *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sess
	if s == nil || s.ctx.Err() != nil {
		return nil
	}
	s.wg.Add(1)
	return s
}

func (a *App) detachSession() *session {
	a.mu.Lock()
	s := a.sess
	a.sess = nil
	a.keyLost = false
	a.mu.Unlock()
	if s != nil {
		s.cancel()
	}
	return s
}

func (a *App) retireSession(s *session) {
	if s == nil {
		return
	}
	done := make(chan struct{})
	a.retireMu.Lock()
	a.retiring = append(a.retiring, done)
	a.retireMu.Unlock()
	go func() {
		defer close(done)
		s.wg.Wait()
		if err := s.db.Close(); err != nil {
			log.Printf("[DB] закрытие: %v", err)
		}
		a.retireMu.Lock()
		for i, ch := range a.retiring {
			if ch == done {
				a.retiring = append(a.retiring[:i], a.retiring[i+1:]...)
				break
			}
		}
		a.retireMu.Unlock()
	}()
}

func (a *App) waitRetired() {
	a.retireMu.Lock()
	pending := append([]chan struct{}(nil), a.retiring...)
	a.retireMu.Unlock()
	if len(pending) == 0 {
		return
	}
	deadline := time.After(retireTimeout)
	for _, ch := range pending {
		select {
		case <-ch:
		case <-deadline:
			log.Printf("[DB] предыдущая сессия ещё закрывается, продолжаем")
			return
		}
	}
}
