package sandboxexec

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestProcessRunsAndWaits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "exit 0")
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}
	process, err := Start(cmd, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() == 0 {
		t.Fatal("process PID was not recorded")
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}
