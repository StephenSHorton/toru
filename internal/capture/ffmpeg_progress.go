package capture

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// RunFFmpegProgress runs ffmpeg like RunFFmpeg, reporting progress (0..1) as
// it encodes. totalMs is the expected output duration; onProgress may be nil.
// It adds "-progress pipe:1 -nostats" itself, so args must not write the
// output to stdout.
func RunFFmpegProgress(ctx context.Context, args []string, totalMs int, onProgress func(float64)) error {
	bin, err := LocateFFmpeg()
	if err != nil {
		return err
	}
	full := append([]string{"-progress", "pipe:1", "-nostats"}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	configureSysProcAttr(cmd)
	stderr := &tailBuffer{max: stderrTailSize}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn ffmpeg: %w", err)
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if onProgress == nil || totalMs <= 0 {
			continue
		}
		// -progress emits key=value lines; out_time_us is the encoded position.
		if v, ok := strings.CutPrefix(sc.Text(), "out_time_us="); ok {
			if us, err := strconv.ParseInt(v, 10, 64); err == nil && us > 0 {
				onProgress(min(float64(us)/1000/float64(totalMs), 1))
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, lastLine(stderr.String()))
	}
	return nil
}
