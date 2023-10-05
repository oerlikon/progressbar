// Package progressbar implements a simple thread-safe console progress bar
// with some basic customization options.
package progressbar

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mitchellh/colorstring"
	"github.com/rivo/uniseg"
	"golang.org/x/term"
)

// ProgressBar is a simple customizable progress bar.
// It is safe for concurrent use by multiple goroutines.
type ProgressBar struct {
	state  state
	config config
	lock   sync.Mutex
}

// State is a summary of a progress bar's current position.
type State struct {
	CurrentPercent float64
	CurrentBytes   float64
	SecondsSince   float64
	SecondsLeft    float64
	KBsPerSecond   float64
}

type state struct {
	currentNum        int64
	currentPercent    int
	lastPercent       int
	currentSaucerSize int

	lastShown time.Time
	startTime time.Time

	counterTime         time.Time
	counterNumSinceLast int64
	counterLastTenRates []float64

	maxLineWidth int
	currentBytes float64
	finished     bool
	stopped      bool

	rendered string
}

type config struct {
	max                int64 // max number of the counter
	maxHumanized       string
	maxHumanizedSuffix string
	width              int
	writer             io.Writer
	theme              Theme
	description        string
	iterationString    string
	ignoreLength       bool // ignoreLength if max bytes not known

	// whether the output is expected to contain color codes
	colorCodes bool

	// show rate of change in kB/sec or MB/sec
	showBytes bool

	// show the iterations per second
	showIterationsPerSecond bool
	showIterationsCount     bool

	// always display total rate
	totalRate bool

	// whether the progress bar should show elapsed time.
	// always enabled if predictTime is true.
	elapsedTime bool

	// whether the progress bar should attempt to estimate the finishing
	// time of the progress based on the start time and the average
	// number of seconds between increments.
	predictTime bool

	// minimum time to wait in between updates
	throttleInterval time.Duration

	// clear bar once finished or stopped
	clearOnFinish bool

	// spinnerType should be a key from the spinners map
	spinnerType int

	// fullWidth specifies whether to measure and set the bar to a specific width
	fullWidth bool

	// visible specifies whether the bar is visible
	visible bool

	// whether the render function should make use of ANSI codes to reduce console I/O
	useANSICodes bool

	// whether the getStringWidth function should be more rigorous
	trickyWidths bool
}

// Theme defines the elements of a progress bar.
type Theme struct {
	Saucer        string
	SaucerHead    string
	SaucerPadding string
	BarStart      string
	BarEnd        string
}

var defaultTheme = Theme{Saucer: "█", SaucerPadding: " ", BarStart: "|", BarEnd: "|"}

var spinners = map[int][]string{
	9:  {"|", "/", "-", "\\"},
	14: {"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	59: {".  ", ":. ", ".:.", " .:", "  .", "   "},
}

// Option is the general type for progress bar customization options.
type Option func(p *ProgressBar)

// OptionWidth sets the width of the progress bar.
func OptionWidth(width int) Option {
	return func(p *ProgressBar) {
		p.config.width = width
	}
}

// OptionSpinnerStyle sets the visual style of the spinner. Default is 9.
//
// Available styles are restricted to 9, 14 and 59.
func OptionSpinnerStyle(style int) Option {
	return func(p *ProgressBar) {
		p.config.spinnerType = style
		p.checkTrickyWidths()
	}
}

// OptionTheme sets the appearance of the elements of the progress bar.
func OptionTheme(theme Theme) Option {
	return func(p *ProgressBar) {
		p.config.theme = theme
		p.checkTrickyWidths()
	}
}

// OptionFullWidth sets the progress bar to full width of the console.
func OptionFullWidth() Option {
	return func(p *ProgressBar) {
		p.config.fullWidth = true
	}
}

// OptionVisible sets whether the bar should be visible.
// On by default, but can be useful to hide the progress bar when output is redirected etc.
func OptionVisible(show bool) Option {
	return func(p *ProgressBar) {
		p.config.visible = show
	}
}

// OptionWriter sets the output writer (defaults to os.Stdout).
func OptionWriter(w io.Writer) Option {
	return func(p *ProgressBar) {
		p.config.writer = w
	}
}

// OptionDescription sets the description label of the progress bar.
func OptionDescription(s string) Option {
	return func(p *ProgressBar) {
		p.config.description = s
		p.checkTrickyWidths()
	}
}

// OptionUseColorCodes enables support for color codes.
func OptionUseColorCodes() Option {
	return func(p *ProgressBar) {
		p.config.colorCodes = true
	}
}

// OptionShowElapsed enables elapsed time display.
func OptionShowElapsed() Option {
	return func(p *ProgressBar) {
		p.config.elapsedTime = true
	}
}

// OptionShowRemaining enables display of estimated remaining time in addition to elapsed time.
//
// Has no effect on spinners as their estimated remaining time is unknown.
func OptionShowRemaining() Option {
	return func(p *ProgressBar) {
		p.config.predictTime = true
	}
}

// OptionShowCount enables display of current count out of total.
func OptionShowCount() Option {
	return func(p *ProgressBar) {
		p.config.showIterationsCount = true
	}
}

// OptionShowIts enables display of iterations/second.
func OptionShowIts() Option {
	return func(p *ProgressBar) {
		p.config.showIterationsPerSecond = true
	}
}

// OptionItsString sets what the iterations are called.
// The default is "it" which would display "it/s".
func OptionItsString(its string) Option {
	return func(p *ProgressBar) {
		p.config.iterationString = its
		p.checkTrickyWidths()
	}
}

// OptionTotalRate enables plain total rate display instead of default recent average rate.
func OptionTotalRate() Option {
	return func(p *ProgressBar) {
		p.config.totalRate = true
	}
}

// OptionThrottle enables minimum time intervals between refreshing the progress bar.
// Default is 0 seconds which makes the progress bar refresh on every update.
func OptionThrottle(interval time.Duration) Option {
	return func(p *ProgressBar) {
		p.config.throttleInterval = interval
	}
}

// OptionClearOnFinish enables clearing the bar after it's finished or stopped.
func OptionClearOnFinish() Option {
	return func(p *ProgressBar) {
		p.config.clearOnFinish = true
	}
}

// OptionShowBytes enables display in units of bytes/sec.
func OptionShowBytes() Option {
	return func(p *ProgressBar) {
		p.config.showBytes = true
	}
}

// OptionUseANSICodes enables use of more optimized terminal I/O.
//
// Only useful in environments with support for ANSI escape sequences.
func OptionUseANSICodes() Option {
	return func(p *ProgressBar) {
		p.config.useANSICodes = true
	}
}

// New constructs a new instance of ProgressBar with specified options.
// With max value of -1 it becomes a spinner.
func New(max int, options ...Option) *ProgressBar {
	return New64(int64(max), options...)
}

// New64 constructs a new instance of ProgressBar with specified options.
// With max value of -1 it becomes a spinner.
func New64(max int64, options ...Option) *ProgressBar {
	b := ProgressBar{
		state: getBasicState(),
		config: config{
			writer:           os.Stdout,
			theme:            defaultTheme,
			iterationString:  "it",
			width:            40,
			max:              max,
			throttleInterval: 0 * time.Nanosecond,
			elapsedTime:      false,
			predictTime:      false,
			spinnerType:      9,
			visible:          true,
		},
	}

	for _, o := range options {
		o(&b)
	}

	if b.config.spinnerType != 9 && b.config.spinnerType != 14 && b.config.spinnerType != 59 {
		panic("invalid spinner type, must be 9 or 14 or 59")
	}

	// ignoreLength if max bytes not known
	if b.config.max == -1 {
		b.config.ignoreLength = true
		b.config.max = int64(b.config.width)
		b.config.predictTime = false
	}

	b.config.maxHumanized, b.config.maxHumanizedSuffix = humanizeBytes(float64(b.config.max))

	b.checkTrickyWidths()

	b.render()

	return &b
}

func getBasicState() state {
	now := time.Now()
	return state{
		startTime:   now,
		counterTime: now,
	}
}

// DefaultBytes creates a new ProgressBar for measuring bytes throughput
// with some reasonable default options.
// With maxBytes value of -1 it becomes a spinner.
func DefaultBytes(maxBytes int64, description ...string) *ProgressBar {
	desc := ""
	if len(description) > 0 {
		desc = description[0]
	}
	return New64(maxBytes,
		OptionDescription(desc),
		OptionWriter(os.Stderr),
		OptionShowBytes(),
		OptionWidth(10),
		OptionThrottle(65*time.Millisecond),
		OptionShowCount(),
		OptionSpinnerStyle(14),
		OptionFullWidth())
}

// Default creates a new ProgressBar with some reasonable default options.
// With max value of -1 it becomes a spinner.
func Default(max int64, description ...string) *ProgressBar {
	desc := ""
	if len(description) > 0 {
		desc = description[0]
	}
	return New64(max,
		OptionDescription(desc),
		OptionWriter(os.Stderr),
		OptionWidth(10),
		OptionThrottle(65*time.Millisecond),
		OptionShowCount(),
		OptionShowIts(),
		OptionSpinnerStyle(14),
		OptionFullWidth())
}

// String returns the current rendering of the progress bar.
func (p *ProgressBar) String() string {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.state.rendered
}

// Reset resets the progress bar to initial state.
func (p *ProgressBar) Reset() {
	p.lock.Lock()
	p.state = getBasicState()
	p.lock.Unlock()
}

// Finish fills the bar to full.
func (p *ProgressBar) Finish() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.state.currentNum = p.config.max
	p.state.lastShown = time.Time{} // re-render regardless of throttling
	return p.add(0)
}

// Stop stops the progress bar at current state.
func (p *ProgressBar) Stop() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.state.stopped = true
	p.state.lastShown = time.Time{} // re-render regardless of throttling
	return p.add(0)
}

// Add adds specified delta to the progressbar current value.
func (p *ProgressBar) Add(delta int) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.add(int64(delta))
}

// Add64 adds specified delta to the progressbar current value.
func (p *ProgressBar) Add64(delta int64) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.add(delta)
}

// Set sets the progressbar's current value.
func (p *ProgressBar) Set(value int) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.add(int64(value) - int64(p.state.currentBytes))
}

// Set64 sets the progressbar's current value.
func (p *ProgressBar) Set64(value int64) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.add(value - int64(p.state.currentBytes))
}

func (p *ProgressBar) add(delta int64) error {
	if p.config.max <= 0 {
		return errors.New("max must be greater than 0")
	}

	if p.state.currentNum < p.config.max {
		if p.config.ignoreLength {
			p.state.currentNum = (p.state.currentNum + delta) % p.config.max
		} else {
			p.state.currentNum += delta
		}
	}

	p.state.currentBytes += float64(delta)

	if !p.config.totalRate {
		// reset the countdown timer approx every second to take rolling average
		p.state.counterNumSinceLast += delta
		if t := time.Since(p.state.counterTime).Seconds(); t > 0.5 {
			rate := float64(p.state.counterNumSinceLast) / t
			p.state.counterLastTenRates = append(p.state.counterLastTenRates, rate)
			if len(p.state.counterLastTenRates) > 10 {
				p.state.counterLastTenRates = p.state.counterLastTenRates[1:]
			}
			p.state.counterTime = time.Now()
			p.state.counterNumSinceLast = 0
		}
	}

	percent := float64(p.state.currentNum) / float64(p.config.max)
	p.state.currentSaucerSize = int(percent * float64(p.config.width))
	p.state.currentPercent = int(percent * 100)
	updateBar := p.state.currentPercent != p.state.lastPercent && p.state.currentPercent > 0

	p.state.lastPercent = p.state.currentPercent
	if p.state.currentNum > p.config.max {
		return errors.New("current number exceeds max")
	}

	// always update if show bytes/second or its/second
	if updateBar || p.config.showIterationsPerSecond || p.config.showIterationsCount || delta == 0 {
		return p.render()
	}

	return nil
}

// Clear erases progress bar from the current line.
func (p *ProgressBar) Clear() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return clearProgressBar(p.config, p.state)
}

// SetDescription changes the description label of the progress bar.
func (p *ProgressBar) SetDescription(s string) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.config.description = s

	p.checkTrickyWidths()

	p.state.lastShown = time.Time{} // re-render regardless of throttling
	p.render()
}

// Max returns the maximum value of the progress bar at which it's considered full.
func (p *ProgressBar) Max() int {
	p.lock.Lock()
	defer p.lock.Unlock()

	return int(p.config.max)
}

// Max64 returns the maximum value of the progress bar at which it's considered full.
func (p *ProgressBar) Max64() int64 {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.config.max
}

// AddMax adds specified delta to the maximum value of the progress bar.
func (p *ProgressBar) AddMax(delta int) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.setMax(p.config.max + int64(delta))
}

// AddMax64 adds specified delta to the maximum value of the progress bar.
func (p *ProgressBar) AddMax64(delta int64) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.setMax(p.config.max + delta)
}

// SetMax sets the maximum value of the progress bar at which it's considered full.
func (p *ProgressBar) SetMax(max int) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.setMax(int64(max))
}

// SetMax64 sets the maximum value of the progress bar at which it's considered full.
func (p *ProgressBar) SetMax64(max int64) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.setMax(max)
}

func (p *ProgressBar) setMax(max int64) error {
	p.config.max = max
	if p.config.showBytes {
		p.config.maxHumanized, p.config.maxHumanizedSuffix = humanizeBytes(float64(p.config.max))
	}
	return p.add(0) // re-render
}

// IsFinished reports whether the progress bar is finished.
func (p *ProgressBar) IsFinished() bool {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.state.finished
}

// render renders the progress bar, updating the maximum
// rendered line width. this function is not thread-safe,
// so it must be called with an acquired lock.
func (p *ProgressBar) render() error {
	// make sure that the rendering is not happening too quickly
	// but always show if the currentNum reaches the max
	if time.Since(p.state.lastShown).Nanoseconds() < p.config.throttleInterval.Nanoseconds() &&
		p.state.currentNum < p.config.max {
		return nil
	}

	if !p.config.useANSICodes {
		// first, clear the existing progress bar
		if err := clearProgressBar(p.config, p.state); err != nil {
			return err
		}
	}

	// check if the progress bar is finished
	if !p.state.finished && (p.state.currentNum >= p.config.max || p.state.stopped) {
		p.state.finished = true
		if !p.config.clearOnFinish {
			renderProgressBar(p.config, &p.state)
			writeString(p.config, "\n")
		}
	}
	if p.state.finished {
		// when using ANSI codes we don't pre-clean the current line
		if p.config.useANSICodes && p.config.clearOnFinish {
			if err := clearProgressBar(p.config, p.state); err != nil {
				return err
			}
		}
		return nil
	}

	// then, re-render the current progress bar
	w, err := renderProgressBar(p.config, &p.state)
	if err != nil {
		return err
	}

	if w > p.state.maxLineWidth {
		p.state.maxLineWidth = w
	}

	p.state.lastShown = time.Now()

	return nil
}

// checkTrickyWidths checks if any progress bar element's width in screen characters
// is different from the number of runes in it, and updates the relevant config variable.
func (p *ProgressBar) checkTrickyWidths() {
	var parts = []string{
		p.config.description,
		p.config.iterationString,
		p.config.theme.Saucer,
		p.config.theme.SaucerHead,
		p.config.theme.SaucerPadding,
		p.config.theme.BarStart,
		p.config.theme.BarEnd,
	}
	if p.config.ignoreLength {
		parts = append(parts, spinners[p.config.spinnerType]...)
	}
	for _, s := range parts {
		if uniseg.StringWidth(s) != utf8.RuneCountInString(s) {
			p.config.trickyWidths = true
			return
		}
	}
	p.config.trickyWidths = false
}

// State returns the current state.
func (p *ProgressBar) State() State {
	p.lock.Lock()
	defer p.lock.Unlock()

	currentNum, currentBytes, max := p.state.currentNum, p.state.currentBytes, p.config.max

	s := State{
		CurrentPercent: float64(currentNum) / float64(max),
		CurrentBytes:   currentBytes,
		SecondsSince:   time.Since(p.state.startTime).Seconds(),
	}
	if p.state.currentNum > 0 {
		s.SecondsLeft = s.SecondsSince / float64(currentNum) * float64(max-currentNum)
	}
	if s.SecondsSince > 0 {
		s.KBsPerSecond = float64(currentBytes) / 1000 / s.SecondsSince
	}
	return s
}

// Regex matching ANSI escape codes.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func getStringWidth(c config, str string, colorize bool) int {
	if c.colorCodes {
		// convert any color codes in the progress bar into respective ANSI codes
		str = colorstring.Color(str)
	}

	// width of the string printed to console
	// does not include carriage return characters
	cleanString := strings.ReplaceAll(str, "\r", "")

	if c.colorCodes {
		// ANSI codes for colors do not take up space in the console output,
		// so they do not count towards the output string width
		cleanString = ansiRegex.ReplaceAllString(cleanString, "")
	}

	if c.trickyWidths {
		return uniseg.StringWidth(cleanString)
	}
	return utf8.RuneCountInString(cleanString)
}

func renderProgressBar(c config, s *state) (int, error) {
	var sb strings.Builder

	// show iteration count in "current/total" iterations format
	if c.showIterationsCount {
		if sb.Len() == 0 {
			sb.WriteString("(")
		} else {
			sb.WriteString(", ")
		}
		if !c.ignoreLength {
			if c.showBytes {
				currentHumanize, currentSuffix := humanizeBytes(s.currentBytes)
				if currentSuffix == c.maxHumanizedSuffix {
					sb.WriteString(fmt.Sprintf("%s/%s %s",
						currentHumanize, c.maxHumanized, c.maxHumanizedSuffix))
				} else {
					sb.WriteString(fmt.Sprintf("%s %s/%s %s",
						currentHumanize, currentSuffix, c.maxHumanized, c.maxHumanizedSuffix))
				}
			} else {
				sb.WriteString(fmt.Sprintf("%.0f/%d", s.currentBytes, c.max))
			}
		} else {
			if c.showBytes {
				currentHumanize, currentSuffix := humanizeBytes(s.currentBytes)
				sb.WriteString(fmt.Sprintf("%s %s", currentHumanize, currentSuffix))
			} else if !s.finished {
				sb.WriteString(fmt.Sprintf("%.0f/%s", s.currentBytes, "?"))
			} else {
				sb.WriteString(fmt.Sprintf("%.0f/%.0f", s.currentBytes, s.currentBytes))
			}
		}
	}

	rate := 0.0
	if len(s.counterLastTenRates) > 0 && !s.finished && !c.totalRate {
		// display recent rolling average rate
		rate = average(s.counterLastTenRates)
	} else if t := time.Since(s.startTime).Seconds(); t > 0 {
		// if no average samples, or if finished, or total rate option is set
		// then display total rate
		rate = s.currentBytes / t
	}

	// format rate as units of bytes per second
	if c.showBytes && rate > 0 && !math.IsInf(rate, 1) {
		if sb.Len() == 0 {
			sb.WriteString("(")
		} else {
			sb.WriteString(", ")
		}
		currentHumanize, currentSuffix := humanizeBytes(rate)
		sb.WriteString(fmt.Sprintf("%s %s/s", currentHumanize, currentSuffix))
	}

	// format rate as iterations per second/minute/hour
	if c.showIterationsPerSecond {
		if sb.Len() == 0 {
			sb.WriteString("(")
		} else {
			sb.WriteString(", ")
		}
		if rate > 1 || rate == 0 {
			sb.WriteString(fmt.Sprintf("%0.0f %s/s", rate, c.iterationString))
		} else if 60*rate > 1 {
			sb.WriteString(fmt.Sprintf("%0.0f %s/min", 60*rate, c.iterationString))
		} else {
			sb.WriteString(fmt.Sprintf("%0.0f %s/h", 3600*rate, c.iterationString))
		}
	}
	if sb.Len() > 0 {
		sb.WriteString(")")
	}

	leftBrac, rightBrac, saucer, saucerHead := "", "", "", ""

	// show time prediction in "current/total" seconds format
	switch {
	case c.predictTime:
		if rate >= 0 && c.max >= s.currentNum && s.currentNum > 0 {
			rightBrac = (time.Duration((float64(c.max-s.currentNum) / rate)) * time.Second).String()
		}
		fallthrough
	case c.elapsedTime:
		leftBrac = (time.Duration(time.Since(s.startTime).Seconds()) * time.Second).String()
	}

	if c.fullWidth && !c.ignoreLength {
		width, err := termWidth()
		if err != nil {
			width = 80
		}

		amend := 1 // an extra space at eol
		switch {
		case leftBrac != "" && rightBrac != "":
			amend += 4 // space, square brackets and colon
		case leftBrac != "" && rightBrac == "":
			amend += 3 // space and square brackets
		case leftBrac == "" && rightBrac != "":
			amend += 3 // space and square brackets
		}
		if sb.Len() > 0 {
			amend += 1 // another space
		}
		if c.description != "" {
			amend += 1 // another space
		}

		c.width = width - getStringWidth(c, c.description, true) - 8 - amend - sb.Len() - len(leftBrac) - len(rightBrac)
		s.currentSaucerSize = int(float64(s.currentPercent) / 100 * float64(c.width))
	}

	if s.currentSaucerSize > 0 {
		if c.ignoreLength {
			saucer = strings.Repeat(c.theme.SaucerPadding, s.currentSaucerSize-1)
		} else {
			saucer = strings.Repeat(c.theme.Saucer, s.currentSaucerSize-1)
		}
		if c.theme.SaucerHead == "" || s.currentSaucerSize == c.width {
			// use the saucer for the saucer head if it hasn't been set
			// to preserve backwards compatibility
			saucerHead = c.theme.Saucer
		} else {
			saucerHead = c.theme.SaucerHead
		}
	}

	/*
		Progress Bar format
		Description % |------        |  (KB/s) (iteration count) (iteration rate) (predict time)
	*/

	repeatAmount := c.width - s.currentSaucerSize
	if repeatAmount < 0 {
		repeatAmount = 0
	}

	str := ""

	if c.ignoreLength {
		if !s.finished {
			dt, st := time.Since(s.startTime).Milliseconds()/100, c.spinnerType
			str = "\r " +
				spinners[st][int(math.Round(math.Mod(float64(dt), float64(len(spinners[st])))))] +
				sp(" ", c.description != "") +
				c.description +
				sp(" ", sb.Len() > 0) +
				sb.String() +
				sp(" [", c.elapsedTime) +
				sp(leftBrac, c.elapsedTime) +
				sp("]", c.elapsedTime) + " "
		} else {
			str = "\r100%" +
				sp(" ", c.description != "") +
				c.description +
				sp(" ", sb.Len() > 0) +
				sb.String() +
				sp(" [", c.elapsedTime) +
				sp(leftBrac, c.elapsedTime) +
				sp("]", c.elapsedTime) + " "
		}
	} else if rightBrac == "" || s.finished {
		str = "\r" +
			c.description +
			sp(" ", c.description != "") +
			fmt.Sprintf("%3d%% ", s.currentPercent) +
			c.theme.BarStart +
			saucer +
			saucerHead +
			strings.Repeat(c.theme.SaucerPadding, repeatAmount) +
			c.theme.BarEnd +
			sp(" ", sb.Len() > 0) +
			sb.String() +
			sp(" [", c.elapsedTime || c.predictTime) +
			sp(leftBrac, c.elapsedTime || c.predictTime) +
			sp("]", c.elapsedTime || c.predictTime) + " "
	} else {
		str = "\r" +
			c.description +
			sp(" ", c.description != "") +
			fmt.Sprintf("%3d%% ", s.currentPercent) +
			c.theme.BarStart +
			saucer +
			saucerHead +
			strings.Repeat(c.theme.SaucerPadding, repeatAmount) +
			c.theme.BarEnd +
			sp(" ", sb.Len() > 0) +
			sb.String() +
			" [" + leftBrac + ":" + rightBrac + "] "
	}
	if c.colorCodes {
		// convert any color codes in the progress bar into the respective ANSI codes
		str = colorstring.Color(str)
	}
	s.rendered = str[1:]

	return getStringWidth(c, str, false), writeString(c, str)
}

func clearProgressBar(c config, s state) error {
	if s.maxLineWidth == 0 {
		return nil
	}
	if c.useANSICodes {
		// write the "clear current line" ANSI escape sequence
		return writeString(c, "\033[2K\r")
	}
	// overwrite the bar with spaces and return back to the beginning of the line
	return writeString(c, "\r"+strings.Repeat(" ", s.maxLineWidth)+"\r")
}

func writeString(c config, str string) error {
	if !c.visible {
		return nil
	}
	if _, err := io.WriteString(c.writer, str); err != nil {
		return err
	}
	if f, ok := c.writer.(*os.File); ok {
		// ignore any errors in Sync(), as stdout
		// can't be synced on some operating systems
		// like Debian 9 (Stretch)
		f.Sync()
	}
	return nil
}

// Reader is an io.Reader with a progress bar.
type Reader struct {
	r   io.Reader
	bar *ProgressBar
}

// NewReader creates a new Reader with a given io.Reader and a progress bar.
func NewReader(r io.Reader, bar *ProgressBar) Reader {
	return Reader{
		r:   r,
		bar: bar,
	}
}

// Read reads buffer p and adds the number of bytes read to the progress bar.
func (r *Reader) Read(p []byte) (n int, err error) {
	n, err = r.r.Read(p)
	r.bar.Add(n)
	return
}

// Close closes the embedded reader if it implements io.Closer and fills progress bar to full.
func (r *Reader) Close() (err error) {
	if closer, ok := r.r.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return err
		}
	}
	return r.bar.Finish()
}

// Write implements io.Writer, just in case.
func (p *ProgressBar) Write(b []byte) (n int, err error) {
	n = len(b)
	return n, p.Add(n)
}

// Read implements io.Reader, just in case.
func (p *ProgressBar) Read(b []byte) (n int, err error) {
	n = len(b)
	return n, p.Add(n)
}

// Close implements io.Closer, just in case.
func (p *ProgressBar) Close() (err error) {
	return p.Finish()
}

func average(xs []float64) float64 {
	total := 0.0
	for _, v := range xs {
		total += v
	}
	return total / float64(len(xs))
}

var sizes = []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}

func humanizeBytes(s float64) (string, string) {
	if s < 10 {
		return fmt.Sprintf("%2.0f", s), sizes[0]
	}
	e := math.Floor(logn(s, 1000))
	val, suffix := math.Floor(s/math.Pow(1000, e)*10+0.5)/10, sizes[int(e)]
	if val < 10 {
		return fmt.Sprintf("%.1f", val), suffix
	}
	return fmt.Sprintf("%.0f", val), suffix
}

func logn(n, b float64) float64 {
	return math.Log(n) / math.Log(b)
}

func sp(s string, p bool) string {
	if p {
		return s
	}
	return ""
}

// termWidth function returns the visible width of the current terminal
// and can be redefined for testing.
var termWidth = func() (width int, err error) {
	width, _, err = term.GetSize(int(os.Stdout.Fd()))
	if err == nil {
		return width, nil
	}
	width, _, err = term.GetSize(int(os.Stderr.Fd()))
	if err == nil {
		return width, nil
	}
	return 0, err
}
