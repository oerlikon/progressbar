package progressbar

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	termWidth = func() (int, error) {
		return 0, os.ErrPermission
	}
	os.Exit(m.Run())
}

func OptionClock(clock func() time.Time) Option {
	return func(p *ProgressBar) {
		p.config.now = clock
	}
}

func BenchmarkRenderSimple(b *testing.B) {
	bar := New64(1e8, OptionWriter(io.Discard), OptionShowIts(),
		OptionDescription("£"))
	for i := 0; i < b.N; i++ {
		bar.Add(1)
	}
}

func BenchmarkRenderTricky(b *testing.B) {
	bar := New64(1e8, OptionWriter(io.Discard), OptionShowIts(),
		OptionDescription("这是一个つの测试"))
	for i := 0; i < b.N; i++ {
		bar.Add(1)
	}
}

func TestBarSlowAdd(t *testing.T) {
	buf, clock := strings.Builder{}, time.Now()
	bar := New(100,
		OptionWidth(10),
		OptionShowIts(),
		OptionShowRemaining(),
		OptionClock(func() time.Time { return clock }),
		OptionWriter(&buf))
	clock = clock.Add(3 * time.Second)
	bar.Add(1)
	if !strings.Contains(buf.String(), "1%") {
		t.Errorf("wrong string: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "20 it/min") {
		t.Errorf("wrong string: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "[3s:") {
		t.Errorf("wrong string: %q", buf.String())
	}
}

func TestBarSmallBytes(t *testing.T) {
	buf, clock := strings.Builder{}, time.Now()
	bar := New64(100000000,
		OptionShowBytes(),
		OptionShowCount(),
		OptionWidth(10),
		OptionClock(func() time.Time { return clock }),
		OptionWriter(&buf))
	for i := 1; i < 10; i++ {
		clock = clock.Add(100 * time.Millisecond)
		bar.Add(1000)
	}
	if !strings.Contains(buf.String(), "9.0 KB/100 MB") {
		t.Errorf("wrong string: %q", buf.String())
	}
	for i := 1; i < 10; i++ {
		clock = clock.Add(10 * time.Millisecond)
		bar.Add(1000000)
	}
	if !strings.Contains(buf.String(), "9.0/100 MB") {
		t.Errorf("wrong string: %q", buf.String())
	}
}

func TestBarFastBytes(t *testing.T) {
	buf, clock := strings.Builder{}, time.Now()
	bar := New64(1e8,
		OptionShowBytes(),
		OptionShowCount(),
		OptionWidth(10),
		OptionClock(func() time.Time { return clock }),
		OptionWriter(&buf))
	clock = clock.Add(time.Millisecond)
	bar.Add(2e7)
	if !strings.Contains(buf.String(), " GB/s)") {
		t.Errorf("wrong string: %q", buf.String())
	}
}

func TestBar(t *testing.T) {
	bar := New(0)
	if err := bar.Add(1); err == nil {
		t.Error("should have an error for 0 bar")
	}
	bar = New(10)
	if err := bar.Add(11); err == nil {
		t.Error("should have an error for adding > bar")
	}
}

func TestState(t *testing.T) {
	bar := New(100, OptionWidth(10))
	bar.Add(10)
	s := bar.State()
	if s.CurrentPercent != 0.1 {
		t.Error(s)
	}
}

func TestBasicSets(t *testing.T) {
	b := New(333, OptionWidth(222), OptionWriter(io.Discard))

	tc := b.config

	if tc.max != 333 {
		t.Errorf("Expected %s to be %d, instead I got %d\n%+v", "max", 333, tc.max, b)
	}
	if tc.width != 222 {
		t.Errorf("Expected %s to be %d, instead I got %d\n%+v", "width", 222, tc.max, b)
	}
}

func TestOptionTheme(t *testing.T) {
	buf := strings.Builder{}
	bar := New(10,
		OptionTheme(Theme{
			Saucer:        "#",
			SaucerPadding: "-",
			BarStart:      ">",
			BarEnd:        "<",
		}),
		OptionWidth(10),
		OptionShowRemaining(),
		OptionWriter(&buf))
	bar.Add(5)
	result := bar.String()
	expect := " 50% >#####-----< [0s:0s] "
	if result != expect {
		t.Errorf("Render miss-match\nResult: %q\nExpect: %q", result, expect)
	}
}

func TestElapsed(t *testing.T) {
	buf := strings.Builder{}
	bar := New(10,
		OptionWidth(10),
		OptionShowElapsed(),
		OptionWriter(&buf))
	bar.Add(2)
	result := bar.String()
	expect := " 20% |██        | [0s] "
	if result != expect {
		t.Errorf("Render miss-match\nResult: %q\nExpect: %q", result, expect)
	}
}

func TestOptionElapsed_spinner(t *testing.T) {
	buf, clock := strings.Builder{}, time.Now()
	bar := New(-1,
		OptionShowElapsed(),
		OptionShowIts(),
		OptionShowCount(),
		OptionClock(func() time.Time { return clock }),
		OptionWriter(&buf))
	clock = clock.Add(1 * time.Second)
	bar.Add(5)
	result := bar.String()
	expect := " - (5/?, 5 it/s) [1s] "
	if result != expect {
		t.Errorf("Render miss-match\nResult: %q\nExpect: %q", result, expect)
	}
}

func TestEstimated(t *testing.T) {
	buf := strings.Builder{}
	bar := New(10,
		OptionWidth(10),
		OptionShowRemaining(),
		OptionWriter(&buf))
	bar.Add(7)
	result := bar.String()
	expect := " 70% |███████   | [0s:0s] "
	if result != expect {
		t.Errorf("Render miss-match\nResult: %q\nExpect: %q", result, expect)
	}
}

func TestSpinnerState(t *testing.T) {
	clock := time.Now()
	bar := New(-1,
		OptionWidth(100),
		OptionClock(func() time.Time { return clock }))
	clock = clock.Add(1 * time.Second)
	bar.Add(10)

	state := bar.State()
	if state.CurrentBytes != 10.0 {
		t.Errorf("Number of bytes mismatched gotBytes %f wantBytes %f", state.CurrentBytes, 10.0)
	}
	if state.CurrentPercent != 0.1 {
		t.Errorf("Percent of bar mismatched got %f want %f", state.CurrentPercent, 0.1)
	}

	kbPerSec := fmt.Sprintf("%2.2f", state.KBsPerSecond)
	if kbPerSec != "0.01" {
		t.Errorf("Speed mismatched got %s want %s", kbPerSec, "0.01")
	}
}

func TestReaderToBuffer(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	urlToGet := "https://dl.google.com/go/go1.14.1.src.tar.gz"
	req, err := http.NewRequest("GET", urlToGet, nil)
	assert.Nil(t, err)
	resp, err := http.DefaultClient.Do(req)
	assert.Nil(t, err)
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	bar := New(int(resp.ContentLength), OptionShowBytes())
	out := io.MultiWriter(buf, bar)
	_, err = io.Copy(out, resp.Body)
	assert.Nil(t, err)

	md5, err := md5sum(buf)
	assert.Nil(t, err)
	assert.Equal(t, "d441819a800f8c90825355dfbede7266", md5)
}

func TestReaderToFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	urlToGet := "https://dl.google.com/go/go1.14.1.src.tar.gz"
	req, err := http.NewRequest("GET", urlToGet, nil)
	assert.Nil(t, err)
	resp, err := http.DefaultClient.Do(req)
	assert.Nil(t, err)
	defer resp.Body.Close()

	f, err := os.CreateTemp("", "progressbar_testfile")
	if err != nil {
		t.Fatal()
	}
	defer os.Remove(f.Name())
	defer f.Close()

	realStdout := os.Stdout
	defer func() { os.Stdout = realStdout }()
	r, fakeStdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = fakeStdout

	bar := DefaultBytes(resp.ContentLength)
	out := io.MultiWriter(f, bar)
	_, err = io.Copy(out, resp.Body)
	assert.Nil(t, err)
	f.Sync()
	f.Seek(0, 0)

	if err := fakeStdout.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "", string(b))

	md5, err := md5sum(f)
	assert.Nil(t, err)
	assert.Equal(t, "d441819a800f8c90825355dfbede7266", md5)
}

func TestReaderToFileUnknownLength(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	urlToGet := "https://dl.google.com/go/go1.14.1.src.tar.gz"
	req, err := http.NewRequest("GET", urlToGet, nil)
	assert.Nil(t, err)
	resp, err := http.DefaultClient.Do(req)
	assert.Nil(t, err)
	defer resp.Body.Close()

	f, err := os.CreateTemp("", "progressbar_testfile")
	if err != nil {
		t.Fatal()
	}
	defer os.Remove(f.Name())
	defer f.Close()

	realStdout := os.Stdout
	defer func() { os.Stdout = realStdout }()
	r, fakeStdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = fakeStdout

	bar := DefaultBytes(-1, " downloading")
	out := io.MultiWriter(f, bar)
	_, err = io.Copy(out, resp.Body)
	assert.Nil(t, err)
	f.Sync()
	f.Seek(0, 0)

	if err := fakeStdout.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "", string(b))

	md5, err := md5sum(f)
	assert.Nil(t, err)
	assert.Equal(t, "d441819a800f8c90825355dfbede7266", md5)
}

func TestConcurrency(t *testing.T) {
	buf := strings.Builder{}
	bar := New(1000, OptionWriter(&buf))
	var wg sync.WaitGroup
	for i := 0; i < 900; i++ {
		wg.Add(1)
		go func(b *ProgressBar, wg *sync.WaitGroup) {
			bar.Add(1)
			wg.Done()
		}(bar, &wg)
	}
	wg.Wait()
	result := bar.state.currentNum
	expect := int64(900)
	assert.Equal(t, expect, result)
}

func TestIterationNames(t *testing.T) {
	b := Default(20)
	tc := b.config

	// Checking for the default iterations per second or "it/s"
	if tc.iterationString != "it" {
		t.Errorf("Expected %s to be %s, instead I got %s", "iterationString", "it", tc.iterationString)
	}

	// Change the default "it/s" to provide context, downloads per second or "dl/s"
	b = New(20, OptionItsString("dl"))
	tc = b.config

	if tc.iterationString != "dl" {
		t.Errorf("Expected %s to be %s, instead I got %s", "iterationString", "dl", tc.iterationString)
	}
}

func TestHumanizeBytes(t *testing.T) {
	amount, suffix := humanizeBytes(float64(12.34) * 1000 * 1000)
	assert.Equal(t, "12 MB", fmt.Sprintf("%s %s", amount, suffix))

	amount, suffix = humanizeBytes(float64(56.78) * 1000 * 1000 * 1000)
	assert.Equal(t, "57 GB", fmt.Sprintf("%s %s", amount, suffix))
}

func md5sum(r io.Reader) (string, error) {
	hash := md5.New()
	_, err := io.Copy(hash, r)
	return hex.EncodeToString(hash.Sum(nil)), err
}

func TestSetDescription(t *testing.T) {
	buf := strings.Builder{}
	bar := New(100, OptionWidth(10), OptionShowRemaining(), OptionWriter(&buf))
	bar.SetDescription("performing axial adjustments")
	bar.Add(10)
	result := buf.String()
	expect := "" +
		"  0% |          | [0s] " +
		"\r                       \r" +
		"performing axial adjustments   0% |          | [0s] " +
		"\r                                                    \r" +
		"performing axial adjustments  10% |█         | [0s:0s] "
	if result != expect {
		t.Errorf("Render miss-match\nResult: %q\nExpect: %q", result, expect)
	}
}

func TestRenderBlankStateWithThrottle(t *testing.T) {
	buf := strings.Builder{}
	bar := New(100,
		OptionWidth(10),
		OptionShowRemaining(),
		OptionThrottle(time.Millisecond),
		OptionWriter(&buf))
	result := bar.String()
	expect := "  0% |          | [0s] "
	if result != expect {
		t.Errorf("Render miss-match\nResult: %q\nExpect: %q", result, expect)
	}
}

func TestOptionFullWidth(t *testing.T) {
	tests := []struct {
		opts     []Option
		expected string
	}{
		{ // 1
			[]Option{},
			"" +
				"  0% |                                                                       | " +
				"\r                                                                               \r" +
				" 10% |███████                                                                | " +
				"\r                                                                               \r" +
				"100% |███████████████████████████████████████████████████████████████████████| ",
		},
		{ // 2
			[]Option{OptionDescription("Progress:")},
			"" +
				"Progress:   0% |                                                             | " +
				"\r                                                                               \r" +
				"Progress:  10% |██████                                                       | " +
				"\r                                                                               \r" +
				"Progress: 100% |█████████████████████████████████████████████████████████████| ",
		},
		{ // 3
			[]Option{OptionShowRemaining()},
			"" +
				"  0% |                                                                  | [0s] " +
				"\r                                                                               \r" +
				" 10% |██████                                                         | [1s:9s] " +
				"\r                                                                               \r" +
				"100% |███████████████████████████████████████████████████████████████| [2s] ",
		},
		{ // 4
			[]Option{OptionShowElapsed()},
			"" +
				"  0% |                                                                  | [0s] " +
				"\r                                                                               \r" +
				" 10% |██████                                                            | [1s] " +
				"\r                                                                               \r" +
				"100% |██████████████████████████████████████████████████████████████████| [2s] ",
		},
		{ // 5
			[]Option{OptionShowIts(), OptionShowRemaining()},
			"" +
				"  0% |                                                         | (0 it/s) [0s] " +
				"\r                                                                               \r" +
				" 10% |█████                                                | (10 it/s) [1s:9s] " +
				"\r                                                                               \r" +
				"100% |█████████████████████████████████████████████████████| (50 it/s) [2s] ",
		},
		{ // 6
			[]Option{OptionShowCount(), OptionShowRemaining()},
			"" +
				"  0% |                                                          | (0/100) [0s] " +
				"\r                                                                               \r" +
				" 10% |█████                                                 | (10/100) [1s:9s] " +
				"\r                                                                               \r" +
				"100% |█████████████████████████████████████████████████████| (100/100) [2s] ",
		},
		{ // 7
			[]Option{OptionDescription("Progress:"), OptionShowIts(), OptionShowCount(), OptionShowRemaining()},
			"" +
				"Progress:   0% |                                        | (0/100, 0 it/s) [0s] " +
				"\r                                                                               \r" +
				"Progress:  10% |███                                | (10/100, 10 it/s) [1s:9s] " +
				"\r                                                                               \r" +
				"Progress: 100% |██████████████████████████████████| (100/100, 50 it/s) [2s] ",
		},
		{ // 8
			[]Option{OptionShowIts(), OptionShowCount(), OptionShowElapsed()},
			"" +
				"  0% |                                                  | (0/100, 0 it/s) [0s] " +
				"\r                                                                               \r" +
				" 10% |████                                            | (10/100, 10 it/s) [1s] " +
				"\r                                                                               \r" +
				"100% |███████████████████████████████████████████████| (100/100, 50 it/s) [2s] ",
		},
		{ // 9
			[]Option{OptionShowIts(), OptionItsString("deg"), OptionShowCount()},
			"" +
				"  0% |                                                      | (0/100, 0 deg/s) " +
				"\r                                                                               \r" +
				" 10% |█████                                               | (10/100, 10 deg/s) " +
				"\r                                                                               \r" +
				"100% |███████████████████████████████████████████████████| (100/100, 50 deg/s) ",
		},
		{ // 10
			[]Option{OptionUseANSICodes(), OptionShowIts(), OptionItsString("ans"), OptionShowCount()},
			"" +
				"\r  0% |                                                      | (0/100, 0 ans/s) \033[0K" +
				"\r 10% |█████                                               | (10/100, 10 ans/s) \033[0K" +
				"\r100% |███████████████████████████████████████████████████| (100/100, 50 ans/s) \033[0K",
		},
	}

	for i, test := range tests {
		test := test
		t.Run(fmt.Sprintf("%d", i+1), func(t *testing.T) {
			t.Parallel()
			buf, clock := strings.Builder{}, time.Now()
			bar := New(100, append(test.opts, []Option{
				OptionFullWidth(),
				OptionClock(func() time.Time { return clock }),
				OptionWriter(&buf),
			}...)...)
			clock = clock.Add(1 * time.Second)
			bar.Add(10)
			clock = clock.Add(1 * time.Second)
			bar.Add(90)
			assert.Equal(t, test.expected, buf.String())
		})
	}
}

func TestSpinners(t *testing.T) {
	tests := []struct {
		opts     []Option
		expected string
	}{
		{ // 1
			[]Option{},
			"" +
				" | " +
				"\r   \r" +
				" / " +
				"\r   \r" +
				"100% \n",
		},
		{ // 2
			[]Option{OptionDescription("Spinning")},
			"" +
				" | Spinning " +
				"\r            \r" +
				" / Spinning " +
				"\r            \r" +
				"100% Spinning \n",
		},
		{ // 3
			[]Option{OptionShowElapsed()},
			"" +
				" | [0s] " +
				"\r        \r" +
				" / [1s] " +
				"\r        \r" +
				"100% [2s] \n",
		},
		{ // 4
			[]Option{OptionShowIts(), OptionShowElapsed()},
			"" +
				" | (0 it/s) [0s] " +
				"\r                 \r" +
				" / (63 it/min) [1s] " +
				"\r                    \r" +
				"100% (32 it/min) [2s] \n",
		},
		{ // 5
			[]Option{OptionShowCount(), OptionShowElapsed()},
			"" +
				" | (0/?) [0s] " +
				"\r              \r" +
				" / (1/?) [1s] " +
				"\r              \r" +
				"100% (1/1) [2s] \n",
		},
		{ // 6
			[]Option{OptionDescription("Throbbing"), OptionShowIts(), OptionShowCount(), OptionShowElapsed()},
			"" +
				" | Throbbing (0/?, 0 it/s) [0s] " +
				"\r                                \r" +
				" / Throbbing (1/?, 63 it/min) [1s] " +
				"\r                                   \r" +
				"100% Throbbing (1/1, 32 it/min) [2s] \n",
		},
		{ // 7
			[]Option{OptionShowIts(), OptionItsString("deg"), OptionSpinnerStyle(59)},
			"" +
				"     (0 deg/s) " +
				"\r               \r" +
				" .   (63 deg/min) " +
				"\r                  \r" +
				"100% (32 deg/min) \n",
		},
	}

	for i, test := range tests {
		test := test
		t.Run(fmt.Sprintf("%d", i+1), func(t *testing.T) {
			t.Parallel()
			buf, clock := strings.Builder{}, time.Now()
			spinner := New(-1, append(test.opts, []Option{
				OptionClock(func() time.Time { return clock }),
				OptionWriter(&buf),
			}...)...)
			clock = clock.Add(950 * time.Millisecond)
			spinner.Add(1)
			clock = clock.Add(900 * time.Millisecond)
			spinner.Finish()
			assert.Equal(t, test.expected, buf.String())
		})
	}
}
