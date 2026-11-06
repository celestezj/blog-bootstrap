package workspace

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

// PortPicker chooses a local TCP port for the temporary server.
type PortPicker interface {
	Pick() (int, error)
}

// FreePortPicker asks the OS for an unused loopback port.
type FreePortPicker struct{}

func (FreePortPicker) Pick() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ProbeFunc requests the preview URL and returns the HTTP status code.
type ProbeFunc func(ctx context.Context, url string) (int, error)

// Server starts the temporary hexo server, waits for HTTP 200 and always
// recycles the whole process tree. It never runs the forever-running
// `npm run preview` script.
type Server struct {
	Starter Starter
	Ports   PortPicker
	Probe   ProbeFunc
	Env     []string
	Logger  *core.Logger
	StepID  string
	Wait    func(time.Duration)
	// Notify receives human-facing progress while the server starts.
	Notify func(string)

	Interval time.Duration
	MaxPolls int
	Attempts int
}

func (s *Server) notify(format string, args ...interface{}) {
	if s.Notify != nil {
		s.Notify(fmt.Sprintf(format, args...))
	}
}

func (s *Server) starter() Starter {
	if s.Starter != nil {
		return s.Starter
	}
	return ExecStarter{}
}

func (s *Server) picker() PortPicker {
	if s.Ports != nil {
		return s.Ports
	}
	return FreePortPicker{}
}

func (s *Server) probe() ProbeFunc {
	if s.Probe != nil {
		return s.Probe
	}
	client := &http.Client{Timeout: 2 * time.Second}
	return func(ctx context.Context, url string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	}
}

func (s *Server) wait(d time.Duration) {
	if s.Wait != nil {
		s.Wait(d)
		return
	}
	time.Sleep(d)
}

// Smoke verifies the built site is served locally, retrying on another free
// port when the first attempt fails (for example when the port was taken).
func (s *Server) Smoke(ctx context.Context, dir, nodePath, hexoMain string) (map[string]string, error) {
	attempts := s.Attempts
	if attempts <= 0 {
		attempts = 2
	}
	maxPolls := s.MaxPolls
	if maxPolls <= 0 {
		// hexo server regenerates the site on startup; hexo-all-minifier on a
		// large blog can take minutes before the port is listening.
		maxPolls = 1200
	}
	interval := s.Interval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}

	lastDetail := ""
	lastOutput := ""
	logPath := ""
	for attempt := 1; attempt <= attempts; attempt++ {
		port, err := s.picker().Pick()
		if err != nil {
			lastDetail = "无法选择空闲端口: " + err.Error()
			continue
		}
		url := fmt.Sprintf("http://127.0.0.1:%d/", port)
		s.notify("启动临时 hexo server（%s）；首次启动会重新生成站点，可能需要 1-3 分钟...", url)
		spec := ProcessSpec{
			Dir:  dir,
			Env:  s.Env,
			Name: nodePath,
			Args: []string{hexoMain, "server", "-p", strconv.Itoa(port), "-i", "127.0.0.1"},
		}
		proc, startErr := s.starter().Start(ctx, spec)
		if startErr != nil {
			lastDetail = "无法启动 hexo server: " + startErr.Error()
			if s.Logger != nil {
				logPath = s.Logger.Append(s.StepID, "hexo server", startErr.Error())
			}
			continue
		}

		ok, detail, output := s.poll(ctx, proc, url, maxPolls, interval)
		killErr := proc.KillTree()
		lastOutput = output
		lastDetail = detail
		if s.Logger != nil {
			logPath = s.Logger.Append(s.StepID, "hexo server :"+strconv.Itoa(port), output)
		}
		if killErr != nil {
			return nil, protocol.Fatal("PREVIEW_CLEANUP_FAILED", "无法回收本地 server 进程树: "+killErr.Error()).
				WithLog(logPath).WithDetails(s.details(lastOutput))
		}
		if ok {
			return map[string]string{
				"port":        strconv.Itoa(port),
				"url":         url,
				"http_status": "200",
				"attempt":     strconv.Itoa(attempt),
			}, nil
		}
	}

	code := "PREVIEW_TIMEOUT"
	if !strings.Contains(lastDetail, "超时") {
		code = "PREVIEW_FAILED"
	}
	return nil, protocol.Fatal(code, "本地预览验证失败: "+lastDetail).
		WithLog(logPath).WithDetails(s.details(lastOutput))
}

func (s *Server) poll(ctx context.Context, proc Process, url string, maxPolls int, interval time.Duration) (bool, string, string) {
	probe := s.probe()
	detail := "尚未连接"
	for i := 0; i < maxPolls; i++ {
		if err := ctx.Err(); err != nil {
			return false, "运行被中断: " + err.Error(), proc.Output()
		}
		select {
		case <-proc.Done():
			return false, "server 进程提前退出", proc.Output()
		default:
		}
		status, err := probe(ctx, url)
		if err == nil && status == http.StatusOK {
			return true, "", proc.Output()
		}
		if err != nil {
			detail = err.Error()
		} else {
			detail = fmt.Sprintf("HTTP %d", status)
		}
		if (i+1)%40 == 0 {
			s.notify("仍在等待预览服务就绪（已检查 %d 次，约 %d 秒，最后状态: %s）",
				i+1, int((time.Duration(i+1) * interval).Seconds()), detail)
		}
		s.wait(interval)
	}
	return false, "等待 HTTP 200 超时（最后状态: " + detail + "）", proc.Output()
}

func (s *Server) details(output string) map[string]string {
	out := map[string]string{}
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		out["output"] = trimmed
	}
	if s.Logger != nil {
		if tail := s.Logger.Tail(s.StepID); tail != "" {
			out["tail"] = tail
		}
	}
	return out
}
