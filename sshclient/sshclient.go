// Package sshclient manages SSH connections and command execution.
package sshclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Client wraps an SSH connection and provides command execution.
type Client struct {
	conn *ssh.Client
	mu   sync.Mutex

	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// Connect establishes an SSH connection.
//
// Supported auth: password (non-empty password) or key file (non-empty keyPath).
// When both are provided, key takes precedence.
func Connect(addr, user, password, keyPath string) (*Client, error) {
	config := &ssh.ClientConfig{
		User:            user,
		Timeout:         10 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	if keyPath != "" {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read key file %s: %w", keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("cannot parse key file: %w", err)
		}
		config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	} else if password != "" {
		config.Auth = []ssh.AuthMethod{ssh.Password(password)}
	} else {
		return nil, fmt.Errorf("no authentication method provided")
	}

	khPath, khErr := knownHostsPath()
	if khErr == nil && fileExists(khPath) {
		kh, err := knownhosts.New(khPath)
		if err == nil {
			config.HostKeyCallback = kh
		}
	}

	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial failed: %w", err)
	}
	return &Client{conn: conn}, nil
}

// RunCommand starts a command on the remote server and streams its combined
// output line by line into the provided callback.
//
// The callback is invoked from a goroutine and must be safe for concurrent use.
// The caller must call Stop() or Wait() to clean up.
func (c *Client) RunCommand(ctx context.Context, cmd string, onLine func(string)) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("a command is already running")
	}
	c.running = true
	c.done = make(chan struct{})
	ctx, c.cancel = context.WithCancel(ctx)
	c.mu.Unlock()

	session, err := c.conn.NewSession()
	if err != nil {
		c.markDone()
		return fmt.Errorf("cannot create session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		c.markDone()
		return fmt.Errorf("cannot get stdout pipe: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		c.markDone()
		return fmt.Errorf("cannot get stderr pipe: %w", err)
	}

	if err := session.Start(cmd); err != nil {
		session.Close()
		c.markDone()
		return fmt.Errorf("cannot start command: %w", err)
	}

	go c.readOutput(ctx, session, stdout, stderr, onLine)
	return nil
}

// markDone marks the client as not running and signals the done channel.
// Must only be called when c.mu is NOT held.
func (c *Client) markDone() {
	c.mu.Lock()
	c.running = false
	done := c.done
	c.mu.Unlock()
	close(done)
}

// readOutput merges stdout and stderr into the callback.
func (c *Client) readOutput(ctx context.Context, session *ssh.Session, stdout, stderr io.Reader, onLine func(string)) {
	defer c.markDone()
	defer func() {
		session.Wait()
		session.Close()
	}()

	// Merge stdout and stderr via concurrent goroutines and a channel
	lines := make(chan string, 1000)
	var wg sync.WaitGroup

	readPipe := func(name string, rd io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(rd)
		scanner.Buffer(make([]byte, 64*1024), 512*1024)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case lines <- fmt.Sprintf("[ssh:%s read error: %v]", name, err):
			case <-ctx.Done():
			}
		}
	}

	wg.Add(2)
	go readPipe("stdout", stdout)
	go readPipe("stderr", stderr)

	// Close lines channel when both readers finish
	go func() {
		wg.Wait()
		close(lines)
	}()

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return
			}
			onLine(line)
		case <-ctx.Done():
			// Send SIGTERM to the remote process
			if err := session.Signal(ssh.SIGTERM); err != nil {
				// If signal fails, kill the whole session
				session.Close()
			}
			// Drain any remaining output in the background
			go func() {
				for range lines {
				}
			}()
			return
		}
	}
}

// Running returns true if a command is currently active.
func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Wait blocks until the currently running command finishes naturally
// (without cancelling it). Returns immediately if nothing is running.
func (c *Client) Wait() {
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Stop terminates the currently running command. It blocks until the
// session has fully cleaned up.
func (c *Client) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Close terminates any running command and closes the SSH connection.
func (c *Client) Close() error {
	c.Stop()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func knownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/.ssh/known_hosts", nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
