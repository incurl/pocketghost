// Package config is the Go port of the legacy TypeScript `core/Settings.ts` and
// `constants.ts` modules. It reads the exact same environment variables so an
// existing PocketHost deployment can be migrated without touching its .env.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/phome"
)

// Config is the fully resolved, immutable runtime configuration.
type Config struct {
	// Identity / environment
	Debug     bool
	IsDev     bool
	Trace     bool
	NODEEnv   string
	Home      string // PH_HOME
	DataRoot  string
	SSLHome   string
	ProjectRt string

	// Networking
	HTTPProtocol  string
	AppURL        string
	ApexDomain    string
	DaemonPort    int
	MothershipNm  string
	MothershipURL string

	// Daemon behaviour
	DaemonPBIdleTTL        time.Duration
	InstanceAPITimeout     time.Duration
	InstanceAPICheckPeriod time.Duration
	LaunchWarn             time.Duration
	StopTimeout            time.Duration
	MaxConcurrentLaunches  int
	InitialPortPoolSize    int
	PortPoolMin            int
	PortPoolMax            int
	NofileSoft             uint64
	NofileHard             uint64

	// Instance limits (OS level, replaces container limits)
	InstanceMemoryMB   int64
	InstanceCPUPercent int
	InstanceDiskMB     int64
	InstanceNofile     uint64

	// Firewall / edge
	IPCIDRList               []string
	UserProxyIPs             []string
	DisableInstanceWebhooks  bool
	DisableFirewallRateLimit bool
	EdgeID                   string
	EdgeRegion               string
	EdgeInternalURL          string
	EdgeApexDomain           string
	EdgeHeartbeat            time.Duration
	FirewallGrace            time.Duration
	FirewallGraceRetry       time.Duration
	FirewallHTTPPort         int
	FirewallHTTPSPort        int
	MirrorDriftCheck         time.Duration
	MirrorStall              time.Duration
	MirrorCatchupMaxGap      time.Duration
	Secret                   string

	// Mothership integration
	MothershipAdminUsername string
	MothershipAdminPassword string
	MothershipSemver        string
	MothershipPort          int
	MothershipMirrorPort    int

	// TLS
	SSLKey   string
	SSLCert  string
	TLSEmail string
	ACME     bool

	// PocketBase binaries
	PocketBaseRoot         string
	AllowedPocketBaseRange string
	PocketBaseDownloadURL  string
	// InstanceBinaryOverride points every tenant at a single binary. It exists
	// for integration tests and single-version appliances.
	InstanceBinaryOverride string

	// FTP / SFTP
	FTPPort        int
	FTPPasvIP      string
	FTPPasvPortMin int
	FTPPasvPortMax int
	SFTPPort       int
	SFTPHostKey    string

	// Backups
	BackupRoot        string
	BackupRetainCount int
	BackupCron        string
	ArchiveRoot       string

	// Cron
	DefaultIdleTTL time.Duration
}

// Getenv returns the environment variable or a fallback.
func Getenv(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return fallback
}

// EnvInt parses an integer environment variable, falling back on parse errors
// exactly like the TypeScript `envi()` helper (parseInt semantics).
func EnvInt(name string, fallback int) int {
	v := Getenv(name, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return n
}

// EnvBool parses a boolean environment variable, mirroring `parseBoolean`.
func EnvBool(name string, fallback bool) bool {
	v := Getenv(name, "")
	if v == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	}
	return fallback
}

// EnvDurationMS parses milliseconds.
func EnvDurationMS(name string, fallbackMillis int) time.Duration {
	return time.Duration(EnvInt(name, fallbackMillis)) * time.Millisecond
}

// EnvCSV parses a comma separated list, dropping empty entries like `mkCsvString`.
func EnvCSV(name string, fallback []string) []string {
	v, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Load builds the configuration from the environment. It is the analogue of
// `RegisterEnvSettingsService()` and performs the same directory creation that
// `mkPath(..., {create:true})` used to do.
func Load() (*Config, error) {
	phome.LoadDotEnv()

	isDev := Getenv("NODE_ENV", "") == "development"
	if isDev && Getenv("PH_SECRET", "") == "" {
		_ = os.Setenv("PH_SECRET", "dev")
	}

	home := Getenv("PH_HOME", filepath.Join(phome.UserDataDir(), "pockethost"))
	dataRoot := Getenv("DATA_ROOT", filepath.Join(home, "data"))
	sslHome := filepath.Join(home, "ssl")

	httpProtocol := Getenv("HTTP_PROTOCOL", map[bool]string{true: "http:", false: "https:"}[isDev])
	apex := Getenv("APEX_DOMAIN", "pockethost.lvh.me")

	c := &Config{
		Debug:     EnvBool("PH_DEBUG", isDev),
		IsDev:     isDev,
		Trace:     EnvBool("TRACE", false),
		NODEEnv:   Getenv("NODE_ENV", "production"),
		Home:      home,
		DataRoot:  dataRoot,
		SSLHome:   sslHome,
		ProjectRt: Getenv("PH_PROJECT_ROOT", mustAbs(".")),

		HTTPProtocol: httpProtocol,
		AppURL:       Getenv("APP_URL", strings.TrimSuffix(httpProtocol, ":")+"//"+apex),
		ApexDomain:   apex,
		DaemonPort:   EnvInt("DAEMON_PORT", 3000),
		MothershipNm: Getenv("MOTHERSHIP_NAME", "mothership"),

		DaemonPBIdleTTL:        EnvDurationMS("DAEMON_PB_IDLE_TTL", 5000),
		InstanceAPITimeout:     EnvDurationMS("PH_INSTANCE_API_TIMEOUT_MS", 60000),
		InstanceAPICheckPeriod: EnvDurationMS("PH_INSTANCE_API_CHECK_MS", 100),
		LaunchWarn:             EnvDurationMS("PH_CONTAINER_LAUNCH_WARN_MS", 200),
		StopTimeout:            time.Duration(EnvInt("PH_CONTAINER_STOP_TIMEOUT_SEC", 5)) * time.Second,
		MaxConcurrentLaunches:  EnvInt("PH_MAX_CONCURRENT_DOCKER_LAUNCHES", 5),
		InitialPortPoolSize:    EnvInt("INITIAL_PORT_POOL_SIZE", 20),
		PortPoolMin:            EnvInt("PH_INSTANCE_PORT_MIN", 30000),
		PortPoolMax:            EnvInt("PH_INSTANCE_PORT_MAX", 40000),
		NofileSoft:             uint64(EnvInt("PH_CONTAINER_NOFILE_SOFT", 65536)),
		NofileHard:             uint64(EnvInt("PH_CONTAINER_NOFILE_HARD", 524288)),

		InstanceMemoryMB:   int64(EnvInt("PH_INSTANCE_MEMORY_MB", 128)),
		InstanceCPUPercent: EnvInt("PH_INSTANCE_CPU_PERCENT", 50),
		InstanceDiskMB:     int64(EnvInt("PH_INSTANCE_DISK_MB", 512)),
		InstanceNofile:     uint64(EnvInt("PH_INSTANCE_NOFILE", 8192)),

		IPCIDRList:               EnvCSV("IPCIDR_LIST", nil),
		UserProxyIPs:             EnvCSV("PH_USER_PROXY_IPS", nil),
		DisableInstanceWebhooks:  !EnvBool("PH_ENABLE_INSTANCE_WEBHOOKS", false) && EnvBool("PH_DISABLE_INSTANCE_WEBHOOKS", isDev),
		DisableFirewallRateLimit: !EnvBool("PH_ENABLE_FIREWALL_RATE_LIMIT", false) && EnvBool("PH_DISABLE_FIREWALL_RATE_LIMIT", isDev),
		EdgeID:                   Getenv("PH_EDGE_ID", hostnameOr("local")),
		EdgeRegion:               Getenv("PH_EDGE_REGION", "sfo"),
		EdgeInternalURL:          Getenv("PH_EDGE_INTERNAL_URL", ""),
		EdgeApexDomain:           Getenv("EDGE_APEX_DOMAIN", apex),
		EdgeHeartbeat:            EnvDurationMS("PH_EDGE_HEARTBEAT_MS", 10_000),
		FirewallGrace:            EnvDurationMS("PH_FIREWALL_DAEMON_GRACE_MS", 60_000),
		FirewallGraceRetry:       EnvDurationMS("PH_FIREWALL_DAEMON_GRACE_RETRY_MS", 500),
		FirewallHTTPPort:         EnvInt("PH_FIREWALL_HTTP_PORT", 80),
		FirewallHTTPSPort:        EnvInt("PH_FIREWALL_HTTPS_PORT", 443),
		MirrorDriftCheck:         EnvDurationMS("PH_MIRROR_DRIFT_CHECK_MS", 60_000),
		MirrorStall:              EnvDurationMS("PH_MIRROR_STALL_MS", 10*60_000),
		MirrorCatchupMaxGap:      EnvDurationMS("PH_MIRROR_CATCHUP_MAX_GAP_MS", 10*60_000),
		Secret:                   Getenv("PH_SECRET", ""),

		MothershipAdminUsername: Getenv("MOTHERSHIP_ADMIN_USERNAME", ""),
		MothershipAdminPassword: Getenv("MOTHERSHIP_ADMIN_PASSWORD", ""),
		MothershipSemver:        Getenv("MOTHERSHIP_SEMVER", "0.41.*"),
		MothershipPort:          EnvInt("MOTHERSHIP_PORT", 8090),
		MothershipMirrorPort:    EnvInt("PH_EDGE_MIRROR_PORT", 3001),

		SSLKey:   Getenv("SSL_KEY", filepath.Join(sslHome, "tls.key")),
		SSLCert:  Getenv("SSL_CERT", filepath.Join(sslHome, "tls.cert")),
		TLSEmail: Getenv("PH_TLS_EMAIL", ""),
		ACME:     EnvBool("PH_TLS_ACME", !isDev),

		PocketBaseRoot:         Getenv("PH_POCKETBASE_ROOT", filepath.Join(home, "pocketbase")),
		AllowedPocketBaseRange: Getenv("PH_ALLOWED_POCKETBASE_SEMVER", "<1.0.0"),
		PocketBaseDownloadURL:  Getenv("PH_POCKETBASE_DOWNLOAD_URL", "https://github.com/pocketbase/pocketbase/releases/download"),
		InstanceBinaryOverride: Getenv("PH_INSTANCE_BINARY", ""),

		FTPPort:        EnvInt("PH_FTP_PORT", 21),
		FTPPasvIP:      Getenv("PH_FTP_PASV_IP", map[bool]string{true: "127.0.0.1", false: "0.0.0.0"}[isDev]),
		FTPPasvPortMin: EnvInt("PH_FTP_PASV_PORT_MIN", 10000),
		FTPPasvPortMax: EnvInt("PH_FTP_PASV_PORT_MAX", 20000),
		SFTPPort:       EnvInt("PH_SFTP_PORT", 2222),
		SFTPHostKey:    Getenv("PH_SFTP_HOST_KEY", filepath.Join(home, "ssh", "host_key")),

		BackupRoot:        Getenv("PH_BACKUP_ROOT", filepath.Join(dataRoot, "backups")),
		BackupRetainCount: EnvInt("PH_BACKUP_RETAIN", 7),
		BackupCron:        Getenv("PH_BACKUP_CRON", "0 3 * * *"),
		ArchiveRoot:       Getenv("PH_ARCHIVE_ROOT", filepath.Join(dataRoot, "archive")),

		DefaultIdleTTL: EnvDurationMS("DAEMON_PB_IDLE_TTL", 5000),
	}

	c.MothershipURL = strings.TrimSuffix(
		Getenv("MOTHERSHIP_URL", strings.TrimSuffix(httpProtocol, ":")+"://"+c.MothershipNm+"."+apex), "/")

	// mkPath({create:true}) equivalents.
	for _, dir := range []string{c.Home, c.DataRoot, c.PocketBaseRoot, c.SSLHome, c.BackupRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("config: create %s: %w", dir, err)
		}
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate enforces the invariants the TypeScript version relied on.
func (c *Config) Validate() error {
	if c.ApexDomain == "" {
		return fmt.Errorf("config: APEX_DOMAIN must not be empty")
	}
	if c.DaemonPort <= 0 || c.DaemonPort > 65535 {
		return fmt.Errorf("config: DAEMON_PORT %d out of range", c.DaemonPort)
	}
	if c.PortPoolMax <= c.PortPoolMin {
		return fmt.Errorf("config: PH_INSTANCE_PORT_MAX (%d) must be greater than PH_INSTANCE_PORT_MIN (%d)", c.PortPoolMax, c.PortPoolMin)
	}
	if c.InstanceMemoryMB <= 0 {
		return fmt.Errorf("config: PH_INSTANCE_MEMORY_MB must be positive")
	}
	return nil
}

// LogSummary mirrors `logConstants()`.
func (c *Config) LogSummary() [][2]string {
	return [][2]string{
		{"DEBUG", strconv.FormatBool(c.Debug)},
		{"IS_DEV", strconv.FormatBool(c.IsDev)},
		{"NODE_ENV", c.NODEEnv},
		{"PH_HOME", c.Home},
		{"DATA_ROOT", c.DataRoot},
		{"APEX_DOMAIN", c.ApexDomain},
		{"APP_URL", c.AppURL},
		{"HTTP_PROTOCOL", c.HTTPProtocol},
		{"DAEMON_PORT", strconv.Itoa(c.DaemonPort)},
		{"DAEMON_PB_IDLE_TTL", strconv.FormatInt(c.DaemonPBIdleTTL.Milliseconds(), 10)},
		{"MOTHERSHIP_URL", c.MothershipURL},
		{"MOTHERSHIP_SEMVER", c.MothershipSemver},
		{"INITIAL_PORT_POOL_SIZE", strconv.Itoa(c.InitialPortPoolSize)},
		{"PH_POCKETBASE_ROOT", c.PocketBaseRoot},
		{"PH_CONTAINER_STOP_TIMEOUT_SEC", strconv.Itoa(int(c.StopTimeout.Seconds()))},
		{"PH_MAX_CONCURRENT_LAUNCHES", strconv.Itoa(c.MaxConcurrentLaunches)},
		{"PH_INSTANCE_MEMORY_MB", strconv.FormatInt(c.InstanceMemoryMB, 10)},
		{"PH_SFTP_PORT", strconv.Itoa(c.SFTPPort)},
		{"PH_FTP_PORT", strconv.Itoa(c.FTPPort)},
		{"SSL_CERT", c.SSLCert},
		{"SSL_KEY", c.SSLKey},
	}
}

// InstanceDataPath is `mkInstanceDataPath(instanceId, ...path)`.
func (c *Config) InstanceDataPath(instanceID string, parts ...string) string {
	return filepath.Join(append([]string{c.DataRoot, "instances", instanceID}, parts...)...)
}

// InstancesRoot is `INSTANCES_ROOT(...)`.
func (c *Config) InstancesRoot(parts ...string) string {
	return filepath.Join(append([]string{c.DataRoot, "instances"}, parts...)...)
}

// MothershipDataRoot is `MOTHERSHIP_DATA_ROOT(...)`.
func (c *Config) MothershipDataRoot(parts ...string) string {
	return filepath.Join(append([]string{c.DataRoot, "mothership"}, parts...)...)
}

// MothershipDataDB is `MOTHERSHIP_DATA_DB()`.
func (c *Config) MothershipDataDB() string {
	return filepath.Join(c.MothershipDataRoot(), "pb_data", "data.db")
}

// PocketBasePath is `PH_POCKETBASE_ROOT(...)`.
func (c *Config) PocketBasePath(parts ...string) string {
	return filepath.Join(append([]string{c.PocketBaseRoot}, parts...)...)
}

// BackupPath returns the per-instance backup directory.
func (c *Config) BackupPath(instanceID string, parts ...string) string {
	return filepath.Join(append([]string{c.BackupRoot, instanceID}, parts...)...)
}

// InstanceHostname is `mkInstanceHostname` — `<subdomain>.<apex>`.
func (c *Config) InstanceHostname(subdomain string) string {
	if subdomain == "" {
		return c.ApexDomain
	}
	return subdomain + "." + c.ApexDomain
}

// InstanceCanonicalHostname is `mkInstanceCanonicalHostname`.
func (c *Config) InstanceCanonicalHostname(id, cname string) string {
	if strings.TrimSpace(cname) != "" {
		return cname
	}
	return id + "." + c.ApexDomain
}

// InstanceURL is `mkInstanceUrl`.
func (c *Config) InstanceURL(subdomain string, parts ...string) string {
	base := strings.TrimSuffix(c.HTTPProtocol, ":") + "://" + c.InstanceHostname(subdomain)
	if len(parts) == 0 {
		return base
	}
	return base + "/" + strings.Join(parts, "/")
}

// SubdomainFromHost extracts the first label of a host that belongs to the apex.
func (c *Config) SubdomainFromHost(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	if host == "" || host == c.ApexDomain {
		return "", false
	}
	suffix := "." + c.ApexDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	sub := strings.TrimSuffix(host, suffix)
	if sub == "" || strings.Contains(sub, ".") {
		return "", false
	}
	return sub, true
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func hostnameOr(fallback string) string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return fallback
	}
	return h
}
