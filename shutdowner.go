package shutdowner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Closer func() error

var _ grpc.UnaryServerInterceptor = (&Shutdowner{}).GRPCUnaryServerInterceptor

func NewShutdowner() *Shutdowner {
	return &Shutdowner{
		true,
		sync.RWMutex{},
		sync.WaitGroup{},
		nil,
	}
}

type Shutdowner struct {
	running bool
	mu      sync.RWMutex
	wg      sync.WaitGroup
	closers []Closer
}

func (s *Shutdowner) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *Shutdowner) GRPCUnaryServerInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	err = s.AddProcess()
	if err != nil {
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer s.DoneProcess()

	return handler(ctx, req)
}

func (s *Shutdowner) HTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := s.AddProcess()
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(err.Error()))
			return
		}
		defer s.DoneProcess()
		next.ServeHTTP(w, r)
	})
}

func (s *Shutdowner) AddCloser(closer Closer) {
	s.closers = append(s.closers, closer)
}

func (s *Shutdowner) AddProcess() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.running {
		return errors.New("application stopped")
	}
	s.wg.Add(1)
	return nil
}

func (s *Shutdowner) Go(process func()) error {
	err := s.AddProcess()
	if err != nil {
		return err
	}
	go func() {
		defer s.DoneProcess()
		process()
	}()
}

func (s *Shutdowner) DoneProcess() {
	s.wg.Done()
}

func (s *Shutdowner) Close() error {
	s.stopHandle()

	s.waitProcesses()

	err := s.closeAll()
	if err != nil {
		return fmt.Errorf("wait processes: %w", err)
	}
}

func (s *Shutdowner) stopHandle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}

func (s *Shutdowner) waitProcesses() {
	s.wg.Wait()
}

func (s *Shutdowner) closeAll() error {
	for i := len(s.closers) - 1; i >= 0; i-- {
		err := s.closers[i]()
		if err != nil {
			return err
		}
	}
	return nil
}
