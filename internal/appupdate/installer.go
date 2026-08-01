package appupdate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	applyUpdateFlag   = "--echocode-apply-update"
	cleanupUpdateFlag = "--echocode-update-cleanup"
)

type installAuthorization struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	CacheDir  string `json:"cacheDir"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"createdAt"`
	Nonce     string `json:"nonce"`
}

func launchReplacement(source, target, cacheDir string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	cacheDir, err = filepath.Abs(cacheDir)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Base(target), "EchoCode.exe") && !strings.EqualFold(filepath.Base(target), "EchoCode") {
		return fmt.Errorf("拒绝替换非 EchoCode 可执行文件: %s", target)
	}
	if !pathWithin(source, cacheDir) {
		return errors.New("更新程序不在 EchoCode 更新目录中")
	}
	if err := verifyTargetDirectoryWritable(filepath.Dir(target)); err != nil {
		return err
	}
	digest, err := fileSHA256(source)
	if err != nil {
		return err
	}
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	authorization := installAuthorization{
		Source:    source,
		Target:    target,
		CacheDir:  cacheDir,
		SHA256:    digest,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Nonce:     hex.EncodeToString(nonceBytes),
	}
	authDir := filepath.Join(cacheDir, "authorization")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return err
	}
	authPath := filepath.Join(authDir, authorization.Nonce+".json")
	payload, err := json.Marshal(authorization)
	if err != nil {
		return err
	}
	if err := os.WriteFile(authPath, payload, 0o600); err != nil {
		return err
	}
	command := exec.Command(source,
		applyUpdateFlag,
		"--authorization", authPath,
		"--parent-pid", strconv.Itoa(os.Getpid()),
	)
	command.Dir = filepath.Dir(source)
	if err := command.Start(); err != nil {
		_ = os.Remove(authPath)
		return err
	}
	return command.Process.Release()
}

// HandleCommandLine runs the tiny updater mode before Wails starts.
func HandleCommandLine(args []string) (handled bool, exitCode int) {
	if !hasFlag(args, applyUpdateFlag) {
		return false, 0
	}
	err := runReplacement(args)
	if err != nil {
		writeUpdateError(args, err)
		return true, 1
	}
	return true, 0
}

// ScheduleCleanup removes the previous executable and staged updater after the
// replacement process has exited. It never accepts paths outside EchoCode's
// update cache or a sibling backup with the expected suffix.
func ScheduleCleanup(args []string) {
	cleanupDir := flagValue(args, cleanupUpdateFlag)
	backup := flagValue(args, "--echocode-update-backup")
	if cleanupDir == "" && backup == "" {
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		cacheRoot, _ := os.UserCacheDir()
		allowedRoot, _ := filepath.Abs(filepath.Join(cacheRoot, "EchoCode", "updates"))
		for attempt := 0; attempt < 30; attempt++ {
			remaining := false
			if cleanupDir != "" {
				absolute, err := filepath.Abs(cleanupDir)
				if err == nil && pathWithin(absolute, allowedRoot) && absolute != allowedRoot {
					if err := os.RemoveAll(absolute); err != nil {
						remaining = true
					}
				}
			}
			if backup != "" && strings.HasSuffix(strings.ToLower(backup), ".echocode-update-backup") {
				if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
					remaining = true
				}
			}
			if !remaining {
				return
			}
			time.Sleep(time.Second)
		}
	}()
}

func runReplacement(args []string) error {
	authPath := flagValue(args, "--authorization")
	if authPath == "" {
		return errors.New("更新授权文件缺失")
	}
	payload, err := os.ReadFile(authPath)
	if err != nil {
		return err
	}
	_ = os.Remove(authPath)
	var authorization installAuthorization
	if err := json.Unmarshal(payload, &authorization); err != nil {
		return err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, authorization.CreatedAt)
	if err != nil || time.Since(createdAt) > 30*time.Minute || time.Until(createdAt) > time.Minute {
		return errors.New("更新授权已过期")
	}
	currentExecutable, err := os.Executable()
	if err != nil {
		return err
	}
	currentExecutable, _ = filepath.Abs(currentExecutable)
	source, _ := filepath.Abs(authorization.Source)
	target, _ := filepath.Abs(authorization.Target)
	cacheDir, _ := filepath.Abs(authorization.CacheDir)
	if !samePath(currentExecutable, source) || !pathWithin(source, cacheDir) {
		return errors.New("更新程序来源校验失败")
	}
	if !strings.EqualFold(filepath.Base(target), "EchoCode.exe") && !strings.EqualFold(filepath.Base(target), "EchoCode") {
		return errors.New("更新目标不是 EchoCode")
	}
	digest, err := fileSHA256(source)
	if err != nil {
		return err
	}
	if !strings.EqualFold(digest, authorization.SHA256) {
		return errors.New("更新程序在启动后发生变化")
	}

	backup := target + ".echocode-update-backup"
	_ = os.Remove(backup)
	renamed := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		err = os.Rename(target, backup)
		if err == nil {
			renamed = true
			break
		}
		if os.IsNotExist(err) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil && !os.IsNotExist(err) && !renamed {
		return fmt.Errorf("等待 EchoCode 退出并替换文件: %w", err)
	}

	temporary := target + ".echocode-update-new"
	_ = os.Remove(temporary)
	if err := copyExecutable(source, temporary); err != nil {
		if renamed {
			_ = os.Rename(backup, target)
		}
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		if renamed {
			_ = os.Rename(backup, target)
		}
		return err
	}

	command := exec.Command(target,
		cleanupUpdateFlag, filepath.Dir(source),
		"--echocode-update-backup", backup,
	)
	command.Dir = filepath.Dir(target)
	if err := command.Start(); err != nil {
		return fmt.Errorf("更新完成但重新启动失败: %w", err)
	}
	return command.Process.Release()
}

func verifyTargetDirectoryWritable(directory string) error {
	probe, err := os.CreateTemp(directory, ".echocode-update-write-test-*")
	if err != nil {
		return fmt.Errorf("EchoCode 所在目录不可写，无法自动更新: %w", err)
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil {
		return removeErr
	}
	return nil
}

func copyExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func pathWithin(path, root string) bool {
	path, errPath := filepath.Abs(filepath.Clean(path))
	root, errRoot := filepath.Abs(filepath.Clean(root))
	if errPath != nil || errRoot != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func flagValue(args []string, flag string) string {
	for index := 0; index < len(args); index++ {
		if args[index] == flag && index+1 < len(args) {
			return strings.TrimSpace(args[index+1])
		}
		if strings.HasPrefix(args[index], flag+"=") {
			return strings.TrimSpace(strings.TrimPrefix(args[index], flag+"="))
		}
	}
	return ""
}

func writeUpdateError(args []string, updateErr error) {
	authPath := flagValue(args, "--authorization")
	directory := filepath.Dir(authPath)
	if directory == "." || directory == "" {
		directory = os.TempDir()
	}
	message := time.Now().Format(time.RFC3339) + " " + updateErr.Error() + "\n"
	file, err := os.OpenFile(filepath.Join(directory, "update-error.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		_, _ = file.WriteString(message)
		_ = file.Close()
	}
}
