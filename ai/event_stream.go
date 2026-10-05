package ai

// Ported from pi (https://github.com/earendil-works/pi), Copyright (c) 2025
// Mario Zechner, MIT License; see THIRD_PARTY_NOTICES.

import (
	"iter"
	"sync"
)

// Port of src/utils/event-stream.ts. pi's EventStream is an async iterable
// with an unbounded queue; this is the same with a mutex and a condition
// variable, so a slow consumer never blocks the producing goroutine.

// EventStream queues events from one producer for one consumer. The first
// event for which isComplete returns true fixes the final result.
type EventStream[T any, R any] struct {
	mu            sync.Mutex
	cond          *sync.Cond
	queue         []T
	done          bool
	resultSet     bool
	result        R
	isComplete    func(T) bool
	extractResult func(T) R
}

func NewEventStream[T any, R any](isComplete func(T) bool, extractResult func(T) R) *EventStream[T, R] {
	s := &EventStream[T, R]{isComplete: isComplete, extractResult: extractResult}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Push queues an event. Events after the stream is done are dropped.
func (s *EventStream[T, R]) Push(event T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	if s.isComplete(event) {
		s.done = true
		s.setResult(s.extractResult(event))
	}
	s.queue = append(s.queue, event)
	s.cond.Broadcast()
}

// End marks the stream finished; result, if given, becomes the final result
// unless one was already set by a completing event.
func (s *EventStream[T, R]) End(result ...R) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	if len(result) > 0 {
		s.setResult(result[0])
	}
	s.cond.Broadcast()
}

func (s *EventStream[T, R]) setResult(r R) {
	if !s.resultSet {
		s.result, s.resultSet = r, true
	}
}

// Next blocks for the next event; ok is false once the stream ended and
// the queue is drained.
func (s *EventStream[T, R]) Next() (event T, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.done {
		s.cond.Wait()
	}
	if len(s.queue) == 0 {
		return event, false
	}
	event = s.queue[0]
	s.queue = s.queue[1:]
	return event, true
}

// All iterates over the events (pi: for await ... of stream).
func (s *EventStream[T, R]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		for {
			ev, ok := s.Next()
			if !ok || !yield(ev) {
				return
			}
		}
	}
}

// Result blocks until the stream is done and returns the final result
// (pi: result()).
func (s *EventStream[T, R]) Result() R {
	s.mu.Lock()
	defer s.mu.Unlock()
	for !s.done {
		s.cond.Wait()
	}
	return s.result
}

// AssistantMessageEventStream completes on "done" or "error".
type AssistantMessageEventStream = EventStream[AssistantMessageEvent, *AssistantMessage]

func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	return NewEventStream(
		func(e AssistantMessageEvent) bool { return e.Type == EventDone || e.Type == EventError },
		func(e AssistantMessageEvent) *AssistantMessage {
			if e.Type == EventDone {
				return e.Message
			}
			return e.Error
		},
	)
}
