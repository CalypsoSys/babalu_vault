package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Settings  SettingsConfig  `yaml:"settings"`
	Retention RetentionPolicy `yaml:"retention"`
	Servers   []ServerConfig  `yaml:"servers"`
	Backups   []BackupConfig  `yaml:"backups"`
}

type SettingsConfig struct {
	TempDir   string `yaml:"temp_dir"`
	RootDir   string `yaml:"root_dir"`
	LogPath   string `yaml:"log_path"`
	GzipLevel int    `yaml:"gzip_level"`
	DryRun    bool   `yaml:"dry_run"`
	TimeOfDay string `yaml:"time_of_day"`
	StatePath string `yaml:"state_path"`
}

type RetentionPolicy struct {
	DailyKeep   int `yaml:"daily_keep"`
	WeeklyKeep  int `yaml:"weekly_keep"`
	MonthlyKeep int `yaml:"monthly_keep"`
}

type ServerConfig struct {
	Name      string `yaml:"name"`
	Type      string `yaml:"type"`
	Host      string `yaml:"host"`
	SSHTarget string `yaml:"ssh_target"`
	SSHPort   int    `yaml:"ssh_port"`
}

type BackupConfig struct {
	Name      string          `yaml:"name"`
	Server    string          `yaml:"server"`
	Engine    string          `yaml:"engine"`
	Source    *SourceConfig   `yaml:"source"`
	Discovery DiscoveryConfig `yaml:"discovery"`
	Targets   []TargetConfig  `yaml:"targets"`
}

type SourceConfig struct {
	Mode        string `yaml:"mode"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Container   string `yaml:"container"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	PasswordEnv string `yaml:"password_env"`
}

type DiscoveryConfig struct {
	Databases bool     `yaml:"databases"`
	Ignore    []string `yaml:"ignore"`
}

type TargetConfig struct {
	Name      string           `yaml:"name"`
	Database  string           `yaml:"database"`
	Path      string           `yaml:"path"`
	Excludes  []string         `yaml:"excludes"`
	Retention *RetentionPolicy `yaml:"retention"`
}

type SelectedTarget struct {
	Server    ServerConfig
	Backup    BackupConfig
	Target    TargetConfig
	Retention RetentionPolicy
}

var envReferencePattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults(filepath.Dir(path))
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) applyDefaults(baseDir string) {
	if c.Settings.GzipLevel == 0 {
		c.Settings.GzipLevel = 6
	}
	if c.Settings.TempDir == "" {
		c.Settings.TempDir = "./tmp"
	}
	if c.Settings.LogPath == "" {
		c.Settings.LogPath = "./logs/babalu-vault.log"
	}
	if c.Settings.TimeOfDay == "" {
		c.Settings.TimeOfDay = "02:00"
	}
	if c.Settings.StatePath == "" {
		c.Settings.StatePath = "./state/babalu-vault-scheduler.json"
	}
	if c.Retention.DailyKeep == 0 {
		c.Retention.DailyKeep = 14
	}
	if c.Retention.WeeklyKeep == 0 {
		c.Retention.WeeklyKeep = 8
	}
	if c.Retention.MonthlyKeep == 0 {
		c.Retention.MonthlyKeep = 12
	}
	c.Settings.TempDir = resolvePath(baseDir, c.Settings.TempDir)
	c.Settings.RootDir = resolvePath(baseDir, c.Settings.RootDir)
	c.Settings.LogPath = resolvePath(baseDir, c.Settings.LogPath)
	c.Settings.StatePath = resolvePath(baseDir, c.Settings.StatePath)
}

func resolvePath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}

func (c *Config) Validate() error {
	if c.Settings.GzipLevel < 1 || c.Settings.GzipLevel > 9 {
		return fmt.Errorf("settings.gzip_level must be between 1 and 9, got %d", c.Settings.GzipLevel)
	}
	if c.Settings.RootDir == "" {
		return errors.New("settings.root_dir is required")
	}
	if _, _, err := c.Settings.ScheduledTimeOfDay(); err != nil {
		return err
	}
	if len(c.Servers) == 0 {
		return errors.New("at least one server must be configured")
	}
	if len(c.Backups) == 0 {
		return errors.New("at least one backup must be configured")
	}

	serversByName := make(map[string]ServerConfig, len(c.Servers))
	for _, server := range c.Servers {
		if server.Name == "" {
			return errors.New("server.name is required")
		}
		if _, exists := serversByName[server.Name]; exists {
			return fmt.Errorf("server %q is configured more than once", server.Name)
		}
		switch server.Type {
		case "tcp":
			if server.Host == "" {
				return fmt.Errorf("server %q host is required for tcp", server.Name)
			}
		case "docker":
		case "ssh":
			if server.SSHTarget == "" {
				return fmt.Errorf("server %q ssh_target is required for ssh", server.Name)
			}
		default:
			return fmt.Errorf("server %q type must be tcp, docker, or ssh", server.Name)
		}
		serversByName[server.Name] = server
	}

	backupNames := make(map[string]struct{}, len(c.Backups))
	for _, backup := range c.Backups {
		if backup.Name == "" {
			return errors.New("backup.name is required")
		}
		if _, exists := backupNames[backup.Name]; exists {
			return fmt.Errorf("backup %q is configured more than once", backup.Name)
		}
		backupNames[backup.Name] = struct{}{}

		server, ok := serversByName[backup.Server]
		if !ok {
			return fmt.Errorf("backup %q references unknown server %q", backup.Name, backup.Server)
		}

		switch backup.Engine {
		case "postgres", "mysql":
			if err := validateDatabaseBackup(server, backup); err != nil {
				return err
			}
		case "files":
			if err := validateFileBackup(server, backup); err != nil {
				return err
			}
		default:
			return fmt.Errorf("backup %q engine must be postgres, mysql, or files", backup.Name)
		}
	}
	return validateRetention(c.Retention, "retention")
}

func validateDatabaseBackup(server ServerConfig, backup BackupConfig) error {
	if backup.Source == nil {
		return fmt.Errorf("backup %q source is required for %s", backup.Name, backup.Engine)
	}
	source := *backup.Source
	switch source.Mode {
	case "tcp":
		if source.Port == 0 {
			return fmt.Errorf("backup %q source.port is required for tcp", backup.Name)
		}
		if server.Type == "docker" {
			return fmt.Errorf("backup %q source.mode tcp cannot use docker server %q", backup.Name, server.Name)
		}
		if server.Type == "ssh" && source.Host == "" {
			return fmt.Errorf("backup %q source.host is required for ssh tcp", backup.Name)
		}
	case "docker":
		if source.Container == "" {
			return fmt.Errorf("backup %q source.container is required for docker", backup.Name)
		}
		if server.Type == "tcp" {
			return fmt.Errorf("backup %q source.mode docker cannot use tcp server %q", backup.Name, server.Name)
		}
	default:
		return fmt.Errorf("backup %q source.mode must be tcp or docker", backup.Name)
	}
	if source.Username == "" {
		return fmt.Errorf("backup %q source.username is required", backup.Name)
	}
	if source.Password != "" && source.PasswordEnv != "" {
		return fmt.Errorf("backup %q source.password and source.password_env are mutually exclusive", backup.Name)
	}
	if source.PasswordEnv != "" {
		if backup.Engine != "mysql" || source.Mode != "docker" {
			return fmt.Errorf("backup %q source.password_env is only supported for mysql docker sources", backup.Name)
		}
		if !envNamePattern.MatchString(source.PasswordEnv) {
			return fmt.Errorf("backup %q source.password_env must be an environment variable name", backup.Name)
		}
	}
	if source.Password == "" && source.PasswordEnv == "" {
		return fmt.Errorf("backup %q source.password is required", backup.Name)
	}
	if source.Password != "" {
		if _, _, err := ResolveConfiguredSecret(source.Password); err != nil {
			return fmt.Errorf("backup %q source.password: %w", backup.Name, err)
		}
	}
	if backup.Engine == "mysql" && backup.Discovery.Databases {
		return fmt.Errorf("backup %q mysql discovery is not supported yet", backup.Name)
	}
	if !backup.Discovery.Databases && len(backup.Targets) == 0 {
		return fmt.Errorf("backup %q must have at least one target or discovery.databases enabled", backup.Name)
	}
	for _, target := range backup.Targets {
		if target.Name == "" {
			return fmt.Errorf("backup %q target name is required", backup.Name)
		}
		if target.Retention != nil {
			if err := validateRetention(*target.Retention, fmt.Sprintf("backup %q target %q", backup.Name, target.Name)); err != nil {
				return err
			}
		}
	}
	for _, name := range backup.Discovery.Ignore {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("backup %q discovery.ignore entries must be non-empty", backup.Name)
		}
	}
	return nil
}

func validateFileBackup(server ServerConfig, backup BackupConfig) error {
	if server.Type != "ssh" {
		return fmt.Errorf("backup %q files engine requires an ssh server", backup.Name)
	}
	if backup.Source != nil {
		return fmt.Errorf("backup %q source is not used for files", backup.Name)
	}
	if len(backup.Targets) == 0 {
		return fmt.Errorf("backup %q must have at least one file target", backup.Name)
	}
	for _, target := range backup.Targets {
		if target.Name == "" {
			return fmt.Errorf("backup %q target name is required", backup.Name)
		}
		if target.Path == "" {
			return fmt.Errorf("backup %q target %q path is required", backup.Name, target.Name)
		}
		if target.Retention != nil {
			if err := validateRetention(*target.Retention, fmt.Sprintf("backup %q target %q", backup.Name, target.Name)); err != nil {
				return err
			}
		}
		for _, pattern := range target.Excludes {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("backup %q target %q excludes entries must be non-empty", backup.Name, target.Name)
			}
		}
	}
	return nil
}

func validateRetention(r RetentionPolicy, label string) error {
	if r.DailyKeep < 0 || r.WeeklyKeep < 0 || r.MonthlyKeep < 0 {
		return fmt.Errorf("%s values must be non-negative", label)
	}
	return nil
}

func (s SettingsConfig) ScheduledTimeOfDay() (int, int, error) {
	if s.TimeOfDay == "" {
		return 0, 0, errors.New("settings.time_of_day is required")
	}
	parsed, err := time.Parse("15:04", s.TimeOfDay)
	if err != nil {
		return 0, 0, fmt.Errorf("settings.time_of_day must use 24-hour HH:MM format, got %q", s.TimeOfDay)
	}
	return parsed.Hour(), parsed.Minute(), nil
}

func (c *Config) Filter(serverName, backupName, targetName string) []SelectedTarget {
	serversByName := c.serversByName()
	var selected []SelectedTarget
	for _, backup := range c.Backups {
		server, ok := serversByName[backup.Server]
		if !ok {
			continue
		}
		if serverName != "" && server.Name != serverName {
			continue
		}
		if backupName != "" && backup.Name != backupName {
			continue
		}
		for _, target := range backup.Targets {
			if targetName != "" && target.Name != targetName {
				continue
			}
			selected = append(selected, SelectedTarget{
				Server:    server,
				Backup:    backup,
				Target:    target,
				Retention: c.RetentionFor(target),
			})
		}
	}
	return selected
}

func (c *Config) RetentionFor(target TargetConfig) RetentionPolicy {
	return c.Retention
}

func (c *Config) ServerConfigFor(name string) (ServerConfig, bool) {
	server, ok := c.serversByName()[name]
	return server, ok
}

func (c *Config) serversByName() map[string]ServerConfig {
	servers := make(map[string]ServerConfig, len(c.Servers))
	for _, server := range c.Servers {
		servers[server.Name] = server
	}
	return servers
}

func ResolveConfiguredSecret(value string) (resolved string, placeholder string, err error) {
	if value == "" {
		return "", "<password>", nil
	}
	matches := envReferencePattern.FindStringSubmatch(value)
	if matches == nil {
		if strings.HasPrefix(value, "$") || strings.Contains(value, "${") {
			return "", "", fmt.Errorf("must use exact ${VARNAME} format for environment references")
		}
		return value, "<password>", nil
	}
	name := matches[1]
	resolved = os.Getenv(name)
	return resolved, "${" + name + "}", nil
}

func (b BackupConfig) TargetConfigFor(name string) (TargetConfig, bool) {
	for _, target := range b.Targets {
		if target.Name == name || target.Database == name {
			return target, true
		}
	}
	return TargetConfig{}, false
}

func (t TargetConfig) DatabaseName() string {
	if t.Database != "" {
		return t.Database
	}
	return t.Name
}
