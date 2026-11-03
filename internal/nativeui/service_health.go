/**
 * [INPUT]: internal/services（ProbePort/ProbePortTCP/ProbeClass/ProbeResult）、paneServiceActivity（service_index.go）、Shell.serviceIndex/paneShellIdle、herdr.Projection
 * [OUTPUT]: Shell.serviceProbePorts（仅扫描 lane）、applyServiceHealth（win.Update 内）、healthToneOf、serviceHealthFacts（serviceRowModels 合成保留行）
 * [POS]: 服务健康三态（2026-11-03 用户拍板：扫描推导 + HTTP 探测，修正
 *   services-surfaces 的"健康检查"非目标）：
 *   🟢 绿 = 有监听端口且探测通过；🟠 橙 = 重启中（端口丢但 shell 忙）、挂起
 *   首轮（端口占着不收连接）、抖动（2 分钟内丢-恢 ≥3 次）；🔴 红 = 退出（端口
 *   丢且 shell 空闲）、HTTP 5xx、挂起连续两轮。探测搭现有扫描节拍（独立 3s
 *   超时；HTTP 端口发 GET，裸 TCP 端口只连接——协议分类 sticky 5 分钟，避免
 *   对数据库灌垃圾字节）；状态机在 win.Update 里对上一轮/这一轮/shell 空闲做
 *   纯推导，标注进 paneServiceActivity，红行保留到 pane 关闭、shell 重新忙碌
 *   或服务回归。无第二扫描循环、无持久化——健康是活体事实。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"context"
	"sync"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/services"
)

// Bounds of the health derivation: the probe batch is its own short
// deadline (it must not stretch the scan), flapping counts transitions
// inside a recent window, and a port's protocol classification sticks so
// bare-TCP services are never fed stray bytes.
const (
	probeBatchTimeout   = 3 * time.Second
	probeWorkers        = 6
	probeMaxPorts       = 24
	probeKindTTL        = 5 * time.Minute
	healthFlapWindow    = 2 * time.Minute
	healthFlapThreshold = 3
	healthHungStrikes   = 2 // consecutive bad probes before red
)

// serviceHealthFact is a pane service's carried health state: the tone the
// square draws, why, and what it was when it stopped listening (retained
// rows need the process and ports after the index drops them).
type serviceHealthFact struct {
	Tone    StatusTone
	Reason  string
	Proc    string
	Ports   []uint16
	Since   time.Time
	Strikes int
	Flaps   []time.Time
}

// probeKindEntry remembers a port's protocol: tcp bars the GET probe.
type probeKindEntry struct {
	tcp bool
	at  time.Time
}

// serviceProbePorts probes the pane-attributed listeners (scan lane only,
// its own short deadline). HTTP-classified ports answer a GET; bare-TCP
// ports are only connected to; unseen ports classify once and stick.
func (s *Shell) serviceProbePorts(ports []uint16) map[uint16]services.ProbeResult {
	s.probeMu.Lock()
	now := time.Now()
	set := make([]uint16, 0, len(ports))
	seen := make(map[uint16]bool, len(ports))
	for _, port := range ports {
		if port == 0 || seen[port] || len(set) >= probeMaxPorts {
			continue
		}
		seen[port] = true
		set = append(set, port)
	}
	tcpByPort := make(map[uint16]bool, len(set))
	for _, port := range set {
		if entry, ok := s.probeKinds[port]; ok && now.Sub(entry.at) < probeKindTTL {
			tcpByPort[port] = entry.tcp
		}
	}
	s.probeMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), probeBatchTimeout)
	defer cancel()
	results := make(map[uint16]services.ProbeResult, len(set))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, probeWorkers)
	for _, port := range set {
		tcp := tcpByPort[port]
		wg.Add(1)
		sem <- struct{}{}
		go func(port uint16, tcp bool) {
			defer wg.Done()
			defer func() { <-sem }()
			var result services.ProbeResult
			if tcp {
				result = services.ProbePortTCP(ctx, port)
			} else {
				result = services.ProbePort(ctx, port)
			}
			mu.Lock()
			results[port] = result
			mu.Unlock()
		}(port, tcp)
	}
	wg.Wait()

	s.probeMu.Lock()
	for port, result := range results {
		tcp := tcpByPort[port]
		switch result.Class {
		case services.ProbeHTTP:
			tcp = false
		case services.ProbeAlive:
			// A GET probe answered with non-HTTP bytes: bare TCP from now
			// on. A connect-only probe answering alive keeps its class.
			if _, known := s.probeKinds[port]; !known || !tcpByPort[port] {
				tcp = true
			}
		default:
			continue // Down/Hung say nothing about the protocol
		}
		s.probeKinds[port] = probeKindEntry{tcp: tcp, at: now}
	}
	s.probeMu.Unlock()
	return results
}

// applyServiceHealth derives this scan's tones (UI lane: it reads the
// previous index and shell-idle facts and rewrites the retention table).
// It annotates next's activities in place so every consumer — sidebar,
// popover, agent cards, the workspace rollup — reads the same verdict.
func (s *Shell) applyServiceHealth(panes []herdr.Pane, next map[string]paneServiceActivity, probes map[uint16]services.ProbeResult, idle map[string]bool, now time.Time) {
	livePanes := make(map[string]bool, len(panes))
	for _, pane := range panes {
		livePanes[pane.ID] = true
		act, serving := next[pane.ID]
		prev, wasServing := s.serviceIndex.Panes[pane.ID]
		fact := s.serviceHealth[pane.ID]

		switch {
		case serving && len(act.Ports) > 0:
			tone, reason := serviceProbeTone(act.Ports, probes, &fact)
			if tone == ToneSuccess && serviceFlapping(fact, now) {
				tone, reason = ToneWarning, "unstable"
			}
			if tone == ToneSuccess {
				fact.Tone, fact.Reason, fact.Strikes, fact.Since = ToneSuccess, "", 0, time.Time{}
			} else if fact.Tone != tone || fact.Reason != reason {
				fact.Since = now
			}
			fact.Tone, fact.Reason = tone, reason
			fact.Proc, fact.Ports = act.Proc, act.Ports
			s.serviceHealth[pane.ID] = fact
			act.HealthTone, act.HealthReason = tone, reason
			next[pane.ID] = act

		case wasServing && len(prev.Ports) > 0:
			// It stopped listening between scans. The shell fact says
			// which story is true: idle = the process group is gone
			// (exited); busy = something still runs (a watch loop
			// restarting it, most likely).
			fact.Flaps = append(fact.Flaps, now)
			fact.Proc, fact.Ports = prev.Proc, prev.Ports
			fact.Tone, fact.Since = ToneWarning, now
			fact.Reason = "restarting"
			if idle[pane.ID] {
				fact.Tone, fact.Reason, fact.Strikes = ToneError, "exited", 0
			}
			s.serviceHealth[pane.ID] = fact

		default:
			// Not listening now or last scan: the retained fact stands —
			// a red "exited" stays until the pane closes, the shell gets
			// busy again (the story went stale), or the service returns.
			if fact.Tone == ToneError && !idle[pane.ID] {
				delete(s.serviceHealth, pane.ID)
			}
		}
	}
	for paneID := range s.serviceHealth {
		if !livePanes[paneID] {
			delete(s.serviceHealth, paneID)
		}
	}
}

// serviceProbeTone grades one pane's ports by their probe results: the
// worst verdict wins. HTTP 5xx is an app error right away; a refused or
// timed-out probe counts a strike (two in a row means the server holds the
// port but is not serving); a healthy answer clears the strikes.
func serviceProbeTone(ports []uint16, probes map[uint16]services.ProbeResult, fact *serviceHealthFact) (StatusTone, string) {
	tone, reason := ToneSuccess, ""
	bad := false
	for _, port := range ports {
		result, ok := probes[port]
		if !ok {
			continue
		}
		switch {
		case result.Class == services.ProbeHTTP && result.Code >= 500:
			if tone != ToneError {
				tone, reason = ToneError, "HTTP "+itoa(result.Code)
			}
		case result.Class == services.ProbeHTTP:
			// Any real status line (2xx/3xx/4xx) means the app answers.
			if tone == ToneSuccess {
				tone, reason = ToneSuccess, ""
			}
			fact.Strikes = 0
		case result.Class == services.ProbeAlive:
			fact.Strikes = 0
			if tone == ToneSuccess {
				tone, reason = ToneSuccess, ""
			}
		default: // ProbeDown, ProbeHung
			bad = true
		}
	}
	if bad {
		fact.Strikes++
		if tone != ToneError {
			if fact.Strikes >= healthHungStrikes {
				tone, reason = ToneError, "no response"
			} else {
				tone, reason = ToneWarning, "not responding"
			}
		}
	}
	return tone, reason
}

// serviceFlapping reports ≥3 green→down transitions inside the recent
// window — a service stuck in a crash loop reads as unstable even while
// its port happens to be up.
func serviceFlapping(fact serviceHealthFact, now time.Time) bool {
	count := 0
	for _, at := range fact.Flaps {
		if now.Sub(at) <= healthFlapWindow {
			count++
		}
	}
	return count >= healthFlapThreshold
}

// healthToneOf resolves the square's tone: the scan's derived health when
// it annotated one, the plain green of a serving listener otherwise.
func healthToneOf(act paneServiceActivity) StatusTone {
	if act.HealthTone == ToneNeutral {
		return ToneSuccess
	}
	return act.HealthTone
}
