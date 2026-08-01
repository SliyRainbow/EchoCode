package browserengine

import "time"

const (
	defaultViewportWidth  = 1280
	defaultViewportHeight = 720
)

// Settings contains the runtime policy consumed by the managed browser.
type Settings struct {
	Enabled                bool
	AllowNetwork           bool
	NativePresentation     bool
	ClearDataPolicy        string
	ScreenshotAnnotations  string
	PasswordManagerEnabled bool
	AutofillContactEnabled bool
	DeveloperCDPAccess     bool
	SitePermissions        []SitePermission
	AutofillProfile        AutofillProfile
}

type SitePermission struct {
	Origin     string
	Camera     string
	Microphone string
	Clipboard  string
}

type AutofillProfile struct {
	FullName      string `json:"fullName"`
	Email         string `json:"email"`
	Phone         string `json:"phone"`
	Organization  string `json:"organization"`
	StreetAddress string `json:"streetAddress"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
}

type Tab struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	URL            string  `json:"url"`
	Loading        bool    `json:"loading"`
	CanGoBack      bool    `json:"canGoBack"`
	CanGoForward   bool    `json:"canGoForward"`
	Error          string  `json:"error,omitempty"`
	ViewportWidth  int     `json:"viewportWidth"`
	ViewportHeight int     `json:"viewportHeight"`
	Dialog         *Dialog `json:"dialog,omitempty"`
}

type Dialog struct {
	Type         string `json:"type"`
	Message      string `json:"message"`
	DefaultValue string `json:"defaultValue,omitempty"`
}

type State struct {
	Available   bool       `json:"available"`
	Running     bool       `json:"running"`
	Engine      string     `json:"engine"`
	RenderMode  string     `json:"renderMode"`
	ActiveTabID string     `json:"activeTabId"`
	Tabs        []Tab      `json:"tabs"`
	Downloads   []Download `json:"downloads"`
	LastError   string     `json:"lastError,omitempty"`
	CDPEnabled  bool       `json:"cdpEnabled"`
}

type Frame struct {
	Tab          Tab       `json:"tab"`
	ImageDataURL string    `json:"imageDataUrl"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	Elements     []Element `json:"elements,omitempty"`
	CapturedAt   string    `json:"capturedAt"`
}

type Element struct {
	Index       int     `json:"index"`
	Selector    string  `json:"selector"`
	Tag         string  `json:"tag"`
	Role        string  `json:"role,omitempty"`
	Name        string  `json:"name,omitempty"`
	Text        string  `json:"text,omitempty"`
	Type        string  `json:"type,omitempty"`
	Placeholder string  `json:"placeholder,omitempty"`
	Href        string  `json:"href,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
}

type Snapshot struct {
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Text       string    `json:"text"`
	Elements   []Element `json:"elements"`
	CapturedAt string    `json:"capturedAt"`
}

type ConsoleEntry struct {
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type NetworkEntry struct {
	Method    string `json:"method"`
	URL       string `json:"url"`
	Status    int64  `json:"status"`
	Type      string `json:"type"`
	Failed    bool   `json:"failed"`
	Error     string `json:"error,omitempty"`
	Timestamp string `json:"timestamp"`
}

type Inspector struct {
	Snapshot Snapshot       `json:"snapshot"`
	Console  []ConsoleEntry `json:"console"`
	Network  []NetworkEntry `json:"network"`
}

type Download struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Filename      string  `json:"filename"`
	Path          string  `json:"path,omitempty"`
	State         string  `json:"state"`
	ReceivedBytes float64 `json:"receivedBytes"`
	TotalBytes    float64 `json:"totalBytes"`
	StartedAt     string  `json:"startedAt"`
}

func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}
