// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/server"
	"github.com/vraxter/vraxter/internal/utils"
)

const (
	daemonStopTimeout  = 8 * time.Second
	daemonStartTimeout = 8 * time.Second
	daemonPollInterval = 75 * time.Millisecond
)

func daemonPIDPath(appDir string) string {
	return filepath.Join(appDir, "vraxter.pid")
}

func writeDaemonPIDFile(appDir string, pid int) error {
	if strings.TrimSpace(appDir) == "" {
		return nil
	}
	return utils.AtomicWriteFile(daemonPIDPath(appDir), []byte(strconv.Itoa(pid)+"\n"), 0600)
}

func readDaemonPIDFile(appDir string) (int, bool) {
	if strings.TrimSpace(appDir) == "" {
		return 0, false
	}
	raw, err := os.ReadFile(daemonPIDPath(appDir))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func removeDaemonPIDFile(appDir string) {
	if strings.TrimSpace(appDir) == "" {
		return
	}
	_ = os.Remove(daemonPIDPath(appDir))
}

// probeDaemonEndpoint checks whether a daemon is currently accepting connections on endpoint
// without opening SQLite or initializing any local engine state.
func probeDaemonEndpoint(endpoint string, timeout time.Duration) bool {
	network, address, _, err := server.ParseEndpoint(endpoint)
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// stopDaemonAndWait sends an authenticated SYSTEM_SHUTDOWN signal over gRPC and waits until
// the listener socket/port closes, the tracked PID terminates, and the Unix socket (if any) is released.
func stopDaemonAndWait(endpoint, key, appDir string, timeout time.Duration) error {
	pid, hasPID := readDaemonPIDFile(appDir)
	isListening := probeDaemonEndpoint(endpoint, 250*time.Millisecond)
	if !isListening && (!hasPID || !isProcessAlive(pid)) {
		return nil
	}

	gClient, err := client.NewGRPCClient(endpoint, key)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		stream, sErr := gClient.ExecuteStream(ctx, "SYSTEM_SHUTDOWN", "internal-mgmt", "", "")
		if sErr == nil {
			for range stream {
			}
		}
		cancel()
		_ = gClient.Close()
	}

	network, address, _, _ := server.ParseEndpoint(endpoint)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stillListening := probeDaemonEndpoint(endpoint, 150*time.Millisecond)
		pidAlive := hasPID && isProcessAlive(pid)
		sockExists := false
		if network == "unix" && address != "" {
			if _, statErr := os.Stat(address); statErr == nil {
				sockExists = true
			}
		}
		if !stillListening && !pidAlive && !sockExists {
			return nil
		}
		time.Sleep(daemonPollInterval)
	}

	return fmt.Errorf("timed out after %s waiting for daemon on %s to stop cleanly", timeout, endpoint)
}

func collectDaemonServerArgs() []string {
	args := []string{"server"}
	if airgapFlag {
		args = append(args, "--airgap")
	}
	if enableHostExecFlag {
		args = append(args, "--enable-host-exec")
	}
	if requireSignedSkillsFlag {
		args = append(args, "--require-signed-skills")
	}
	if verboseFlag {
		args = append(args, "--verbose")
	}
	if strings.TrimSpace(listenTCPFlag) != "" {
		args = append(args, "--listen-tcp", strings.TrimSpace(listenTCPFlag))
	} else if strings.TrimSpace(addrFlag) != "" {
		args = append(args, "--addr", strings.TrimSpace(addrFlag))
	}
	if strings.TrimSpace(tlsCAFlag) != "" {
		args = append(args, "--tls-ca", strings.TrimSpace(tlsCAFlag))
	}
	if strings.TrimSpace(tlsCertFlag) != "" {
		args = append(args, "--tls-cert", strings.TrimSpace(tlsCertFlag))
	}
	if strings.TrimSpace(tlsKeyFlag) != "" {
		args = append(args, "--tls-key", strings.TrimSpace(tlsKeyFlag))
	}
	return args
}

// startDetachedDaemon spawns a real detached OS child process running `vraxter server`
// (surviving parent CLI exit) and waits until the endpoint begins accepting connections.
func startDetachedDaemon(exePath string, args []string, endpoint string, timeout time.Duration) (int, error) {
	if strings.TrimSpace(exePath) == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil {
			return 0, fmt.Errorf("failed resolving vraxter executable path: %w", err)
		}
	}

	cmd := exec.Command(exePath, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	configureDetachedProcess(cmd)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed spawning detached vraxter daemon: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probeDaemonEndpoint(endpoint, 150*time.Millisecond) {
			return pid, nil
		}
		if !isProcessAlive(pid) {
			return 0, fmt.Errorf("detached daemon process (PID %d) exited prematurely before listening on %s", pid, endpoint)
		}
		time.Sleep(daemonPollInterval)
	}

	return 0, fmt.Errorf("timed out after %s waiting for detached daemon (PID %d) to listen on %s", timeout, pid, endpoint)
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the Vraxter gRPC background daemon",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Vraxter background daemon as a detached process",
	Run: func(cmd *cobra.Command, args []string) {
		ep := client.DefaultEndpoint()
		if probeDaemonEndpoint(ep, 250*time.Millisecond) {
			fmt.Printf("✅ Vraxter daemon is already running on %s\n", ep)
			return
		}
		pid, err := startDetachedDaemon("", collectDaemonServerArgs(), ep, daemonStartTimeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to start daemon: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🚀 Vraxter daemon started (PID %d) on %s\n", pid, ep)
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check whether the Vraxter background daemon is running",
	Run: func(cmd *cobra.Command, args []string) {
		ep := client.DefaultEndpoint()
		pid, hasPID := readDaemonPIDFile(appConfig.AppDir)
		if probeDaemonEndpoint(ep, 250*time.Millisecond) {
			if hasPID && isProcessAlive(pid) {
				fmt.Printf("🟢 Vraxter daemon is RUNNING (PID %d) on %s\n", pid, ep)
			} else {
				fmt.Printf("🟢 Vraxter daemon is RUNNING on %s\n", ep)
			}
			return
		}
		fmt.Printf("⚪ Vraxter daemon is STOPPED (endpoint %s unreachable)\n", ep)
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Gracefully shut down the running background daemon",
	Run: func(cmd *cobra.Command, args []string) {
		ep := client.DefaultEndpoint()
		if !probeDaemonEndpoint(ep, 250*time.Millisecond) {
			fmt.Println("⚪ Daemon is not running or unreachable.")
			return
		}
		key := ensureDaemonKey()
		fmt.Printf("🛑 Stopping Vraxter daemon on %s... ", ep)
		if err := stopDaemonAndWait(ep, key, appConfig.AppDir, daemonStopTimeout); err != nil {
			fmt.Println("[FAILED]")
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[STOPPED]")
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Stop the active daemon cleanly and relaunch it as a detached process",
	Run: func(cmd *cobra.Command, args []string) {
		ep := client.DefaultEndpoint()
		if probeDaemonEndpoint(ep, 250*time.Millisecond) {
			key := ensureDaemonKey()
			fmt.Printf("🛑 Stopping active daemon on %s... ", ep)
			if err := stopDaemonAndWait(ep, key, appConfig.AppDir, daemonStopTimeout); err != nil {
				fmt.Println("[FAILED]")
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
				os.Exit(1)
			}
			fmt.Println("[STOPPED]")
		}

		fmt.Printf("♻️  Starting detached daemon on %s... ", ep)
		pid, err := startDetachedDaemon("", collectDaemonServerArgs(), ep, daemonStartTimeout)
		if err != nil {
			fmt.Println("[FAILED]")
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[READY] (PID %d)\n", pid)
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd, daemonStatusCmd, daemonStopCmd, daemonRestartCmd)
	rootCmd.AddCommand(daemonCmd)
}
