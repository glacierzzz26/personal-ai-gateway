package proxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// errCloseReader 一直阻塞的 Read,由 Close 打断(模拟上游挂住不动)。
type errCloseReader struct {
	closed chan struct{}
	once   sync.Once
	block  bool
}

func newBlockingReader() *errCloseReader {
	return &errCloseReader{closed: make(chan struct{}), block: true}
}

func (r *errCloseReader) Read(p []byte) (int, error) {
	if !r.block {
		return 0, io.EOF
	}
	<-r.closed
	return 0, net.ErrClosed
}

func (r *errCloseReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

// chunkReader 按固定间隔吐 n 块,每块 1 字节;吐完后再阻塞,直到 Close。
type chunkReader struct {
	n     int
	gap   time.Duration
	done  int
	mu    sync.Mutex
	close chan struct{}
	once  sync.Once
}

func newChunkReader(n int, gap time.Duration) *chunkReader {
	return &chunkReader{n: n, gap: gap, close: make(chan struct{})}
}

func (r *chunkReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	n, done := r.n, r.done
	r.mu.Unlock()
	if done < n {
		time.Sleep(r.gap)
		r.mu.Lock()
		r.done++
		r.mu.Unlock()
		p[0] = 'x'
		return 1, nil
	}
	<-r.close
	return 0, net.ErrClosed
}

func (r *chunkReader) Close() error {
	r.once.Do(func() { close(r.close) })
	return nil
}

// guardReader 把 chunkReader 包成「读到 Close 打断后返回 net.ErrClosed」的形态。
type guardReader struct{ *chunkReader }

// TestStallGuardSurvivesSlowStream 回归:流式「首字节看门狗」不得在数据持续到达时重置成
// 「每次读取间隔」看门狗。旧实现在收到任何数据后都会把 timer 置回 nil,使下一次 Read 又重新
// 武装 —— 于是任何一次超过 timeout 的正常停顿都被掐断并记成 502(生产实测 18 条)。
// 这里让数据以 40ms 间隔持续到达,总时长远超首字节窗口;读到的应是数据而非超时。
func TestStallGuardSurvivesSlowStream(t *testing.T) {
	const first = 60 * time.Millisecond
	r := newChunkReader(10, 40*time.Millisecond) // 10 块 × 40ms = 400ms ≫ first
	g := newStallGuard(r, first, 2*time.Second)
	defer g.Close()

	buf := make([]byte, 8)
	got := 0
	deadline := time.After(5 * time.Second)
	for got < 10 {
		type res struct {
			n   int
			err error
		}
		ch := make(chan res, 1)
		go func() { n, err := g.Read(buf); ch <- res{n, err} }()
		select {
		case v := <-ch:
			if v.err != nil {
				t.Fatalf("读到第 %d 块后中断(err=%v):首字节看门狗被中途重新武装了", got, v.err)
			}
			got += v.n
		case <-deadline:
			t.Fatalf("5s 内只读到 %d 块", got)
		}
	}
}

// TestStallGuardFirstByteTimeout 上游拿到响应头后一个字节都不吐 → 首字节超时。
func TestStallGuardFirstByteTimeout(t *testing.T) {
	r := newBlockingReader()
	g := newStallGuard(r, 30*time.Millisecond, time.Second)
	defer g.Close()

	start := time.Now()
	_, err := g.Read(make([]byte, 8))
	if !errors.Is(err, errFirstByteTimeout) {
		t.Fatalf("err = %v, want errFirstByteTimeout", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("首字节超时耗时 %v,远超设定窗口", d)
	}
}

// TestStallGuardIdleTimeout 流已开始,中途长时间无数据 → 静默超时(宽松窗口)。
func TestStallGuardIdleTimeout(t *testing.T) {
	r := newChunkReader(1, 5*time.Millisecond) // 先来一块,随后永久停住
	g := newStallGuard(r, 40*time.Millisecond, 60*time.Millisecond)
	defer g.Close()

	buf := make([]byte, 8)
	if _, err := g.Read(buf); err != nil {
		t.Fatalf("首块应当正常读到,err=%v", err)
	}
	// 首块之后应当按 idle 窗口(而非 first 窗口)判定 —— 故等 40ms < idle 60ms 时仍不该断。
	time.Sleep(40 * time.Millisecond)
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	go func() { n, err := g.Read(buf); ch <- res{n, err} }()
	select {
	case v := <-ch:
		if !errors.Is(v.err, errStreamIdle) {
			t.Fatalf("err = %v, want errStreamIdle", v.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("中途静默未触发 idle 超时")
	}
}

// TestStreamIdleAtLeastMin 中途静默窗口不随请求超时收窄(流式一旦开始再掐断无法换渠道)。
func TestStreamIdleAtLeastMin(t *testing.T) {
	if minStreamIdle < time.Minute {
		t.Fatalf("minStreamIdle = %v,过小:上游正常的长思考停顿会被误判", minStreamIdle)
	}
}
