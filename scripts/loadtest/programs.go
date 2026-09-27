package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// web is how the simulated browsers and the checks reach the programs: many
// connections kept open, never through a proxy.
// A server run with -tls has a self-signed certificate; the laptops pin it,
// and the test itself only needs to reach it.
var web = &http.Client{
	Timeout: time.Minute,
	Transport: &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        4096,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     time.Minute,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	},
}

// probe asks whether a program answers yet.
var probe = &http.Client{
	Timeout:   2 * time.Second,
	Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

// program is one tam-server or tam-client under test.
type program struct {
	name string
	path string
	args []string
	env  []string
	dir  string // its data folder, which also gets its console output
	host string // the address clients pair with
	port string
	url  string

	// A server running on another machine (-server): not started here, and
	// killed and started again only through the -kill and -restart commands.
	external          bool
	killCmd, startCmd string

	up atomic.Bool // started and not stopped since, as far as the test knows

	mu      sync.Mutex
	cmd     *exec.Cmd
	exited  chan struct{}
	cpu     time.Duration // CPU time of the runs that have ended
	peak    uint64        // the highest peak memory seen, in bytes
	runs    int
	stopped bool // the last stop was a clean one
}

func newProgram(name, path, dir, port string, args, env []string) (*program, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &program{
		name: name, path: path, dir: dir, host: "127.0.0.1", port: port,
		url:  "http://127.0.0.1:" + port,
		args: append([]string{"-addr", "127.0.0.1:" + port}, args...),
		env:  append([]string{"TAM_DATA_DIR=" + dir}, env...),
	}, nil
}

// start runs the program and waits until its API answers. A port the last
// run held can take a moment to be free again, so a start is tried a few
// times.
func (p *program) start() error {
	if p.external {
		return p.startExternal()
	}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = p.startOnce(); err == nil {
			p.up.Store(true)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

func (p *program) startOnce() error {
	out, err := os.OpenFile(filepath.Join(p.dir, "console.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(p.path, p.args...)
	cmd.Env = append(os.Environ(), p.env...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		out.Close()
		return fmt.Errorf("start %s: %w", p.name, err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		out.Close()
		p.mu.Lock()
		if st := cmd.ProcessState; st != nil {
			p.cpu += st.UserTime() + st.SystemTime()
		}
		p.mu.Unlock()
		close(exited)
	}()
	p.mu.Lock()
	p.cmd, p.exited = cmd, exited
	p.runs++
	p.mu.Unlock()

	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case <-exited:
			return fmt.Errorf("%s stopped while starting; see %s", p.name, filepath.Join(p.dir, "console.log"))
		default:
		}
		if answers(p.url) {
			return nil
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			<-exited
			return fmt.Errorf("%s did not answer within 20s", p.name)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// answers reports whether a program answers at base.
func answers(base string) bool {
	res, err := probe.Get(base + "/api")
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// externalServer is the -server one.
func externalServer(o options) (*program, error) {
	u, err := url.Parse(o.server)
	if err != nil {
		return nil, err
	}
	return &program{
		name: "tam-server at " + u.Host, host: u.Hostname(), port: u.Port(),
		url:      u.Scheme + "://" + u.Host,
		external: true, killCmd: o.kill, startCmd: o.restart,
	}, nil
}

// startExternal starts a server elsewhere through the -restart command when
// it is not answering, and waits until it answers.
func (p *program) startExternal() error {
	if !answers(p.url) && p.startCmd != "" {
		if err := shell(p.startCmd); err != nil {
			return fmt.Errorf("-restart: %w", err)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for !answers(p.url) {
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer within 30s", p.name)
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.up.Store(true)
	return nil
}

// current returns the process of the last start and the channel closed
// when it ends; both nil before the first start.
func (p *program) current() (*exec.Cmd, chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd, p.exited
}

// sample notes the peak memory the running process has reached.
func (p *program) sample() {
	cmd, exited := p.current()
	if cmd == nil {
		return
	}
	select {
	case <-exited:
		return
	default:
	}
	if peak, ok := peakMemory(cmd.Process.Pid); ok {
		p.mu.Lock()
		p.peak = max(p.peak, peak)
		p.mu.Unlock()
	}
}

// kill ends the program at once, like a power cut.
func (p *program) kill() {
	p.up.Store(false)
	if p.external {
		if p.killCmd != "" {
			if err := shell(p.killCmd); err != nil {
				fmt.Println("  -kill:", err)
			}
		}
		return
	}
	p.sample()
	cmd, exited := p.current()
	if cmd == nil {
		return
	}
	cmd.Process.Kill()
	<-exited
}

// shutDown stops a tam-client the way its Shut Down button does and kills
// it when that does not work within ten seconds.
func (p *program) shutDown() {
	p.up.Store(false)
	p.sample()
	cmd, exited := p.current()
	if cmd == nil {
		return
	}
	select {
	case <-exited:
		return
	default:
	}
	req, _ := http.NewRequest(http.MethodPost, p.url+"/api/shutdown", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if res, err := probe.Do(req); err == nil {
		res.Body.Close()
	}
	select {
	case <-exited:
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		<-exited
	}
}

// usage returns the CPU time of the ended runs, the highest peak memory
// seen and the number of runs.
func (p *program) usage() (cpu time.Duration, peak uint64, runs int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cpu, p.peak, p.runs
}

// freePorts returns n ports that are free on the loopback interface. The
// listeners are all held until every port is known, so no port comes back
// twice.
func freePorts(n int) ([]string, error) {
	var lns []net.Listener
	defer func() {
		for _, ln := range lns {
			ln.Close()
		}
	}()
	ports := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		lns = append(lns, ln)
		ports = append(ports, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	}
	return ports, nil
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// build compiles tam-server and tam-client from this checkout into dir.
func build(dir string) (server, client string, err error) {
	if _, err := os.Stat("go.mod"); err != nil {
		return "", "", errors.New("run this from the repository root: go run ./scripts/loadtest")
	}
	if _, err := os.Stat(filepath.Join("cmd", "tam-client", "dist", "index.html")); err != nil {
		return "", "", errors.New("the web app is not built yet: run pnpm install and pnpm build in frontend/ first")
	}
	server = filepath.Join(dir, "tam-server"+exeSuffix())
	client = filepath.Join(dir, "tam-client"+exeSuffix())
	for _, b := range [][2]string{{server, "./cmd/tam-server"}, {client, "./cmd/tam-client"}} {
		cmd := exec.Command("go", "build", "-o", b[0], b[1])
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			return "", "", fmt.Errorf("go build %s: %w", b[1], err)
		}
	}
	return server, client, nil
}

// programsIn returns the two programs of a folder given with -bin. The
// paths are made absolute: a bare name would be looked up in PATH.
func programsIn(dir string) (server, client string, err error) {
	if dir, err = filepath.Abs(dir); err != nil {
		return "", "", err
	}
	server = filepath.Join(dir, "tam-server"+exeSuffix())
	client = filepath.Join(dir, "tam-client"+exeSuffix())
	for _, p := range []string{server, client} {
		if _, err := os.Stat(p); err != nil {
			return "", "", fmt.Errorf("-bin: %w", err)
		}
	}
	return server, client, nil
}

// randomPassword returns a server password for this run only.
func randomPassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "loadtest-" + hex.EncodeToString(b)
}
