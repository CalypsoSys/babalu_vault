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
	Backup    BackupConfig    `yaml:"backup"`
	Retention RetentionPolicy `yaml:"retention"`
	Servers   []ServerConfig  `yaml:"servers"`
}

type BackupConfig struct {
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
	Name              string           `yaml:"name"`
	Type              string           `yaml:"type"`
	Host              string           `yaml:"host"`
	Port              int              `yaml:"port"`
	Username          string           `yaml:"username"`
	Password          string           `yaml:"password"`
	Container         string           `yaml:"container"`
	SSHTarget         string           `yaml:"ssh_target"`
	SSHPort           int              `yaml:"ssh_port"`
	SSHRemoteType     string           `yaml:"ssh_remote_type"`
	DiscoverDatabases bool             `yaml:"discover_databases"`
	IgnoreDatabases   []string         `yaml:"ignore_databases"`
	Databases         []DatabaseConfig `yaml:"databases"`
}

type DatabaseConfig struct {
	Name      string           `yaml:"name"`
	Retention *RetentionPolicy `yaml:"retention"`
}

var envReferencePattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

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
	if c.Backup.GzipLevel == 0 {
		c.Backup.GzipLevel = 6
	}
	if c.Backup.TempDir == "" {
		c.Backup.TempDir = "./tmp"
	}
	if c.Backup.LogPath == "" {
		c.Backup.LogPath = "./logs/babalu-vault.log"
	}
	if c.Backup.TimeOfDay == "" {
		c.Backup.TimeOfDay = "02:00"
	}
	if c.Backup.StatePath == "" {
		c.Backup.StatePath = "./state/babalu-vault-scheduler.json"
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
	c.Backup.TempDir = resolvePath(baseDir, c.Backup.TempDir)
	c.Backup.RootDir = resolvePath(baseDir, c.Backup.RootDir)
	c.Backup.LogPath = resolvePath(baseDir, c.Backup.LogPath)
	c.Backup.StatePath = resolvePath(baseDir, c.Backup.StatePath)
}

func resolvePath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}

func (c *Config) Validate() error {
	if c.Backup.GzipLevel < 1 || c.Backup.GzipLevel > 9 {
		return fmt.Errorf("backup.gzip_level must be between 1 and 9, got %d", c.Backup.GzipLevel)
	}
	if c.Backup.RootDir == "" {
		return errors.New("backup.root_dir is required")
	}
	if _, _, err := c.Backup.ScheduledTimeOfDay(); err != nil {
		return err
	}
	if len(c.Servers) == 0 {
		return errors.New("at least one server must be configured")
	}
	for _, server := range c.Servers {
		if server.Name == "" {
			return errors.New("server.name is required")
		}
		if server.Username == "" {
			return fmt.Errorf("server %q username is required", server.Name)
		}
		if server.Password == "" {
			return fmt.Errorf("server %q password is required", server.Name)
		}
		if _, _, err := ResolveConfiguredSecret(server.Password); err != nil {
			return fmt.Errorf("server %q password: %w", server.Name, err)
		}
		if !server.DiscoverDatabases && len(server.Databases) == 0 {
			return fmt.Errorf("server %q must have at least one database or discover_databases enabled", server.Name)
		}
		switch server.Type {
		case "tcp":
			if server.Host == "" {
				return fmt.Errorf("server %q host is required for tcp", server.Name)
			}
			if server.Port == 0 {
				return fmt.Errorf("server %q port is required for tcp", server.Name)
			}
		case "docker":
			if server.Container == "" {
				return fmt.Errorf("server %q container is required for docker", server.Name)
			}
		case "ssh":
			if server.SSHTarget == "" {
				return fmt.Errorf("server %q ssh_target is required for ssh", server.Name)
			}
			switch server.SSHRemoteType {
			case "", "tcp":
				if server.Host == "" {
					return fmt.Errorf("server %q host is required for ssh remote tcp", server.Name)
				}
				if server.Port == 0 {
					return fmt.Errorf("server %q port is required for ssh remote tcp", server.Name)
				}
			case "docker":
				if server.Container == "" {
					return fmt.Errorf("server %q container is required for ssh remote docker", server.Name)
				}
			default:
				return fmt.Errorf("server %q ssh_remote_type must be tcp or docker", server.Name)
			}
		default:
			return fmt.Errorf("server %q type must be tcp, docker, or ssh", server.Name)
		}
		for _, db := range server.Databases {
			if db.Name == "" {
				return fmt.Errorf("server %q database name is required", server.Name)
			}
			if db.Retention != nil {
				if err := validateRetention(*db.Retention, fmt.Sprintf("server %q database %q", server.Name, db.Name)); err != nil {
					return err
				}
			}
		}
		for _, name := range server.IgnoreDatabases {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("server %q ignore_databases entries must be non-empty", server.Name)
			}
		}
	}
	return validateRetention(c.Retention, "retention")
}

func validateRetention(r RetentionPolicy, label string) error {
	if r.DailyKeep < 0 || r.WeeklyKeep < 0 || r.MonthlyKeep < 0 {
		return fmt.Errorf("%s values must be non-negative", label)
	}
	return nil
}

func (b BackupConfig) ScheduledTimeOfDay() (int, int, error) {
	if b.TimeOfDay == "" {
		return 0, 0, errors.New("backup.time_of_day is required")
	}
	parsed, err := time.Parse("15:04", b.TimeOfDay)
	if err != nil {
		return 0, 0, fmt.Errorf("backup.time_of_day must use 24-hour HH:MM format, got %q", b.TimeOfDay)
	}
	return parsed.Hour(), parsed.Minute(), nil
}

func (c *Config) Filter(serverName, databaseName string) []SelectedDatabase {
	var selected []SelectedDatabase
	for _, server := range c.Servers {
		if serverName != "" && server.Name != serverName {
			continue
		}
		for _, database := range server.Databases {
			if databaseName != "" && database.Name != databaseName {
				continue
			}
			selected = append(selected, SelectedDatabase{
				Server:    server,
				Database:  database,
				Retention: c.RetentionFor(database),
			})
		}
	}
	return selected
}

func (c *Config) RetentionFor(database DatabaseConfig) RetentionPolicy {
	return c.Retention
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

type SelectedDatabase struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Retention RetentionPolicy
}

func (s ServerConfig) DatabaseConfigFor(name string) (DatabaseConfig, bool) {
	for _, db := range s.Databases {
		if db.Name == name {
			return db, true
		}
	}
	return DatabaseConfig{}, false
}
