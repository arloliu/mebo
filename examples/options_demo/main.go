package main

import (
	"fmt"
	"log"

	"github.com/arloliu/mebo/internal/options"
)

// Example 1: Database Connection Configuration
type DBConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	Timeout  int
	SSL      bool
}

// DBOption defines the option type for database configuration
type DBOption = options.Option[*DBConfig]

// Helper functions that create options
func WithHost(host string) DBOption {
	return options.NoError(func(config *DBConfig) {
		config.Host = host
	})
}

func WithPort(port int) DBOption {
	return options.New(func(config *DBConfig) error {
		if port <= 0 || port > 65535 {
			return fmt.Errorf("invalid port: %d", port)
		}
		config.Port = port

		return nil
	})
}

func WithCredentials(username, password string) DBOption {
	return options.New(func(config *DBConfig) error {
		if username == "" {
			return fmt.Errorf("username cannot be empty")
		}
		config.Username = username
		config.Password = password

		return nil
	})
}

func WithSSL(enabled bool) DBOption {
	return options.NoError(func(config *DBConfig) {
		config.SSL = enabled
	})
}

func WithTimeout(timeout int) DBOption {
	return options.New(func(config *DBConfig) error {
		if timeout < 0 {
			return fmt.Errorf("timeout cannot be negative")
		}
		config.Timeout = timeout

		return nil
	})
}

// NewDBConfig creates a new database configuration with options
func NewDBConfig(opts ...DBOption) (*DBConfig, error) {
	config := &DBConfig{
		Host:    "localhost",
		Port:    5432,
		Timeout: 30,
		SSL:     false,
	}

	if err := options.Apply(config, opts...); err != nil {
		return nil, err
	}

	return config, nil
}

// Example 2: Server Configuration
type ServerConfig struct {
	Address    string
	MaxClients int
	Debug      bool
	LogLevel   string
}

type ServerOption = options.Option[*ServerConfig]

func WithAddress(addr string) ServerOption {
	return options.NoError(func(config *ServerConfig) {
		config.Address = addr
	})
}

func WithMaxClients(maxClients int) ServerOption {
	return options.New(func(config *ServerConfig) error {
		if maxClients <= 0 {
			return fmt.Errorf("max clients must be positive")
		}
		config.MaxClients = maxClients

		return nil
	})
}

func WithDebug(debug bool) ServerOption {
	return options.NoError(func(config *ServerConfig) {
		config.Debug = debug
	})
}

func WithLogLevel(level string) ServerOption {
	return options.New(func(config *ServerConfig) error {
		validLevels := []string{"debug", "info", "warn", "error"}
		for _, valid := range validLevels {
			if level == valid {
				config.LogLevel = level
				return nil
			}
		}

		return fmt.Errorf("invalid log level: %s", level)
	})
}

func NewServerConfig(opts ...ServerOption) (*ServerConfig, error) {
	config := &ServerConfig{
		Address:    ":8080",
		MaxClients: 100,
		Debug:      false,
		LogLevel:   "info",
	}

	if err := options.Apply(config, opts...); err != nil {
		return nil, err
	}

	return config, nil
}

func main() {
	// Example 1: Database Configuration
	fmt.Println("=== Database Configuration Example ===")

	// Create with default values
	dbConfig1, err := NewDBConfig()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Default config: %+v\n", dbConfig1)

	// Create with custom options
	dbConfig2, err := NewDBConfig(
		WithHost("prod-db.example.com"),
		WithPort(3306),
		WithCredentials("admin", "secret123"),
		WithSSL(true),
		WithTimeout(60),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Custom config: %+v\n", dbConfig2)

	// Example with error handling
	_, err = NewDBConfig(
		WithPort(-1), // This should cause an error
	)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	}

	fmt.Println("\n=== Server Configuration Example ===")

	// Server configuration with options
	serverConfig, err := NewServerConfig(
		WithAddress(":9000"),
		WithMaxClients(500),
		WithDebug(true),
		WithLogLevel("debug"),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Server config: %+v\n", serverConfig)

	// Example with validation error
	_, err = NewServerConfig(
		WithLogLevel("invalid"), // This should cause an error
	)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	}

	fmt.Println("\n=== Benefits of Generic Options Pattern ===")
	fmt.Println("1. Type-safe: Each option is strongly typed to its target struct")
	fmt.Println("2. Reusable: The same pattern works for any struct type")
	fmt.Println("3. Extensible: Easy to add new options without breaking existing code")
	fmt.Println("4. Error handling: Options can validate inputs and return errors")
	fmt.Println("5. Composable: Options can be stored, passed around, and composed")
	fmt.Println("6. Clean API: Users get a fluent, readable configuration interface")
}
