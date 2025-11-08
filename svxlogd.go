// svxlogd.go
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	outDir      = flag.String("dir", "/var/log/svxlink", "directory to write logs")
	prefix      = flag.String("prefix", "log_svxlink_", "log filename prefix")
	flushPeriod = flag.Int("flush", 10, "flush to disk every N seconds")
	compress    = flag.Bool("compress", true, "compress rotated (old) logs")
	keepDays    = flag.Int("keep", 14, "how many compressed backups to keep (older removed)")
	jctlUnit    = flag.String("unit", "svxlink.service", "systemd unit name for journalctl -u <unit> -f")
	restartWait = flag.Int("restart-wait", 3, "seconds to wait before restarting journalctl if it exits")
)

const version = "1.0.0"

type logger struct {
	dir    string
	prefix string

	mu     sync.Mutex
	f      *os.File
	w      *bufio.Writer
	curDay string // YYYY-MM-DD
}

/**
 * getFilenameForDay returns the log filename for given day (YYYY-MM-DD)
 */
func (l *logger) getFilenameForDay(day string) string {
	return filepath.Join(l.dir, fmt.Sprintf("%s%s.txt", l.prefix, day))
}

/**
 * openForDay opens (or reuses) the log file for given day (YYYY-MM-DD)
 */
func (l *logger) openForDay(day string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.curDay == day && l.f != nil {
		return nil
	}

	// close old
	if l.w != nil {
		l.w.Flush()
	}
	if l.f != nil {
		l.f.Sync()
		l.f.Close()
	}

	fn := l.getFilenameForDay(day)
	if err := os.MkdirAll(l.dir, 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(fn, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	l.f = f
	l.w = bufio.NewWriterSize(f, 64*1024)
	l.curDay = day
	return nil
}

/**
 * writeLine writes a line to the current log file
 */
func (l *logger) writeLine(line string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return fmt.Errorf("writer not opened")
	}
	_, err := l.w.WriteString(line + "\n")
	return err
}

/**
 * flush flushes current log file to disk
 */
func (l *logger) flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		if err := l.w.Flush(); err != nil {
			return err
		}
	}
	if l.f != nil {
		return l.f.Sync()
	}
	return nil
}

/**
 * close closes the current log file
 */
func (l *logger) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		_ = l.w.Flush()
	}
	if l.f != nil {
		_ = l.f.Sync()
		_ = l.f.Close()
	}
	l.w = nil
	l.f = nil
	l.curDay = ""
}

// compressFile gzips `path` and writes `path.gz`, then removes original.
// Returns path of gz file or error.
func compressFile(path string) (string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()

	outPath := path + ".gz"
	tmpPath := outPath + ".tmp"

	out, err := os.Create(tmpPath)
	if err != nil {
		return "", err
	}

	gw := gzip.NewWriter(out)
	gw.Name = filepath.Base(path)
	gw.ModTime = time.Now()

	_, err = io.Copy(gw, in)
	_ = gw.Close()
	_ = out.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	// move tmp to final
	if err := os.Rename(tmpPath, outPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	// remove original
	if err := os.Remove(path); err != nil {
		// not fatal, but return error
		return outPath, fmt.Errorf("compressed but failed removing original: %w", err)
	}

	return outPath, nil
}

// cleanupOld archives: keep only last keepDays of compressed logs matching prefix
func cleanupOld(dir, prefix string, keep int) error {
	if keep <= 0 {
		return nil
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	// find *.txt.gz matching prefix
	var gzFiles []os.DirEntry
	for _, e := range dirEntries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".txt.gz") {
			gzFiles = append(gzFiles, e)
		}
	}
	// sort by name (date in name => lexical sorts by date) newest last
	// We want to remove oldest beyond keep
	if len(gzFiles) <= keep {
		return nil
	}
	// sort ascending
	sortFn := func(i, j int) bool { return gzFiles[i].Name() < gzFiles[j].Name() }
	// simple insertion sort (few files)
	for i := 1; i < len(gzFiles); i++ {
		for j := i; j > 0 && sortFn(j, j-1); j-- {
			gzFiles[j], gzFiles[j-1] = gzFiles[j-1], gzFiles[j]
		}
	}
	toRemove := gzFiles[:len(gzFiles)-keep]
	for _, e := range toRemove {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	return nil
}

/**
 * runJournalctl starts journalctl -u <unit> -f -o cat and returns its stdout pipe
 * and the command object. The caller is responsible for stopping the command.
 */
func runJournalctl(unit string, stop <-chan struct{}) (io.ReadCloser, *exec.Cmd, error) {
	// journalctl -u <unit> -f -o cat
	cmd := exec.Command("journalctl", "-u", unit, "-f", "-o", "short-iso")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	// monitor stop signal: if stop requested, kill the process
	go func() {
		<-stop
		_ = cmd.Process.Kill()
	}()

	return stdout, cmd, nil
}

func main() {
	flag.Parse()

	fmt.Printf("Démarrage de 'svxlogd' version %s\n", version)
	fmt.Printf("Logging svxlink unit '%s' vers le dossier '%s' avec le préfixe '%s'\n", *jctlUnit, *outDir, *prefix)
	fmt.Printf("Flush periode en secondes            : %v\n", flushPeriod)
	fmt.Printf("Compression des logs rotatifs        : %v\n", *compress)
	fmt.Printf("Jours de logs compressés à conserver : %v\n", *keepDays)
	fmt.Printf("RestartWait                          : %v\n", restartWait)

	// ensure output directory
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create outdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	logger := &logger{
		dir:    *outDir,
		prefix: *prefix,
	}

	// start flush ticker
	flushTicker := time.NewTicker(time.Duration(*flushPeriod) * time.Second)
	defer flushTicker.Stop()

	// signal handling for graceful shutdown
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	// stop channel to kill journalctl subprocess
	jStop := make(chan struct{})
	defer close(jStop)

	// loop that (re)starts journalctl and consumes lines
	go func() {
		for {
			stdout, cmd, err := runJournalctl(*jctlUnit, jStop)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to start journalctl: %v (retry in %d sec)\n", err, *restartWait)
				time.Sleep(time.Duration(*restartWait) * time.Second)
				continue
			}

			scanner := bufio.NewScanner(stdout)
			// set a large buffer for long log lines
			const maxBuf = 1024 * 1024
			buf := make([]byte, 0, 64*1024)
			scanner.Buffer(buf, maxBuf)

			for scanner.Scan() {
				line := scanner.Text()

				// open log for current day
				now := time.Now()
				day := now.Format("2006-01-02")
				if err := logger.openForDay(day); err != nil {
					fmt.Fprintf(os.Stderr, "openForDay error: %v\n", err)
					// continue nonetheless
				}

				if err := logger.writeLine(line); err != nil {
					fmt.Fprintf(os.Stderr, "writeLine error: %v\n", err)
				}

				// If date changed (rare mid-line boundary), handle rotation/compression.
				// We'll check by local time occasionally in the flush ticker as well.
			}

			// scanner ended (journalctl exited)
			if err := scanner.Err(); err != nil {
				fmt.Fprintf(os.Stderr, "journalctl scanner error: %v\n", err)
			}

			// attempt to wait the command (gives it a chance to exit cleanly)
			_ = cmd.Wait()

			// If stop requested by program shutdown, break loop.
			select {
			case <-jStop:
				return
			default:
			}

			// restart after short sleep
			time.Sleep(time.Duration(*restartWait) * time.Second)
			fmt.Fprintf(os.Stderr, "journalctl terminated, restarting...\n")
		}
	}()

	// main control loop: flush and handle day changes, compression, cleanup
	for {
		select {
		case <-flushTicker.C:
			// flush current file
			if err := logger.flush(); err != nil {
				fmt.Fprintf(os.Stderr, "flush error: %v\n", err)
			}

			// handle rotation by date (if date changed since open)
			now := time.Now()
			day := now.Format("2006-01-02")
			logger.mu.Lock()
			current := logger.curDay
			logger.mu.Unlock()
			if current != "" && current != day {
				// close previous day and optionally compress it
				prevFile := logger.getFilenameForDay(current)

				// close logger -> next open will create new file
				logger.close()

				if *compress {
					if _, err := os.Stat(prevFile); err == nil {
						fmt.Fprintf(os.Stderr, "compressing %s\n", prevFile)
						if _, err := compressFile(prevFile); err != nil {
							fmt.Fprintf(os.Stderr, "compress error %v\n", err)
						}
					}
				}

				// cleanup old compressed files
				if err := cleanupOld(*outDir, *prefix, *keepDays); err != nil {
					fmt.Fprintf(os.Stderr, "cleanup error: %v\n", err)
				}
			}

		case s := <-sigc:
			fmt.Fprintf(os.Stderr, "signal %v received, shutting down...\n", s)
			// stop journalctl subprocess by closing channel
			close(jStop)
			// final flush/close
			_ = logger.flush()
			logger.close()
			return
		}
	}
}
