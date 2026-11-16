package config

import (
    "fmt"
    "os"
    "time"

    "gopkg.in/yaml.v3"
)

type ServerConfig struct {
    Host        string `yaml:"host"`
    BasePort    int    `yaml:"base_port"`
    HTTPPort    int    `yaml:"http_port"`
    OutputDir   string `yaml:"output_dir"`
    ChunkTime   int    `yaml:"chunk_time"`
    Encoder     string `yaml:"encoder"`
    FrameDetail bool   `yaml:"frame_detail"`
}

type CameraConfig struct {
    ID         string `yaml:"id"`
    Name       string `yaml:"name"`
    RTSPURL    string `yaml:"rtsp_url"`
    PortOffset int    `yaml:"port_offset"`
    FPS        int    `yaml:"fps"`
    Bitrate    int    `yaml:"bitrate"`
    Width      int    `yaml:"width"`
    Height     int    `yaml:"height"`
    Preset     string `yaml:"preset"`
    Tune       string `yaml:"tune"`
    KeyInt     int    `yaml:"keyint"`
    BFrames    int    `yaml:"bframes"`
    Enabled    bool   `yaml:"enabled"`
}

type RabbitMQConfig struct {
    Enabled  bool   `yaml:"enabled"`
    Host     string `yaml:"host"`
    Port     int    `yaml:"port"`
    User     string `yaml:"user"`
    Password string `yaml:"password"`
    Exchange string `yaml:"exchange"`
}

type Config struct {
    Server   ServerConfig   `yaml:"server"`
    Cameras  []CameraConfig `yaml:"cameras"`
    RabbitMQ RabbitMQConfig `yaml:"rabbitmq"`

    // SessionTimestamp is set at runtime, not from YAML
    SessionTimestamp string
}

func Load(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("cannot read config file %q: %w", path, err)
    }
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("cannot parse config file: %w", err)
    }
    applyDefaults(&cfg)
    if err := validate(&cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

// SetSessionTimestamp generates a new session timestamp (format: 20060102_150405)
func (c *Config) SetSessionTimestamp() {
    c.SessionTimestamp = time.Now().Format("20060102_150405")
}

func (c *Config) EnabledCameras() []CameraConfig {
    var out []CameraConfig
    for _, cam := range c.Cameras {
        if cam.Enabled {
            out = append(out, cam)
        }
    }
    return out
}

func (c *Config) SRTPort(cam CameraConfig) int {
    return c.Server.BasePort + cam.PortOffset
}

func (c *Config) SRTCallerURI(cam CameraConfig) string {
    return fmt.Sprintf("srt://%s:%d?mode=caller&latency=200&timeout=10000000&transtype=live&streamid=%s",
        c.Server.Host, c.SRTPort(cam), cam.ID)
}

func (c *Config) SRTListenerURI(cam CameraConfig) string {
    return fmt.Sprintf("srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live",
        c.SRTPort(cam))
}

// CameraOutputDir now includes the session timestamp subdirectory
func (c *Config) CameraOutputDir(cam CameraConfig) string {
    if c.SessionTimestamp == "" {
        // Fallback for safety (should not happen)
        return fmt.Sprintf("%s/%s", c.Server.OutputDir, cam.ID)
    }
    return fmt.Sprintf("%s/%s/%s", c.Server.OutputDir, cam.ID, c.SessionTimestamp)
}

func applyDefaults(cfg *Config) {
    if cfg.Server.BasePort == 0 {
        cfg.Server.BasePort = 9000
    }
    if cfg.Server.HTTPPort == 0 {
        cfg.Server.HTTPPort = 8080
    }
    if cfg.Server.ChunkTime == 0 {
        cfg.Server.ChunkTime = 120
    }
    if cfg.Server.Encoder == "" {
        cfg.Server.Encoder = "h265"
    }
    if cfg.Server.OutputDir == "" {
        cfg.Server.OutputDir = "./recordings"
    }
    for i := range cfg.Cameras {
        cam := &cfg.Cameras[i]
        if cam.FPS == 0 {
            cam.FPS = 25
        }
        if cam.Bitrate == 0 {
            cam.Bitrate = 900
        }
        if cam.Width == 0 {
            cam.Width = 1920
        }
        if cam.Height == 0 {
            cam.Height = 1080
        }
        if cam.Preset == "" {
            cam.Preset = "ultrafast"
        }
        if cam.Tune == "" {
            cam.Tune = "zerolatency"
        }
        if cam.KeyInt == 0 {
            cam.KeyInt = 30
        }
    }
    if cfg.RabbitMQ.Exchange == "" {
        cfg.RabbitMQ.Exchange = "camera_events"
    }
    if cfg.RabbitMQ.Port == 0 {
        cfg.RabbitMQ.Port = 5672
    }
    if cfg.RabbitMQ.Host == "" {
        cfg.RabbitMQ.Host = "localhost"
    }
    if cfg.RabbitMQ.User == "" {
        cfg.RabbitMQ.User = "guest"
    }
    if cfg.RabbitMQ.Password == "" {
        cfg.RabbitMQ.Password = "guest"
    }
}

func validate(cfg *Config) error {
    if cfg.Server.Host == "" {
        return fmt.Errorf("server.host is required in config.yaml")
    }
    for i, cam := range cfg.Cameras {
        if cam.ID == "" {
            return fmt.Errorf("cameras[%d]: id is required", i)
        }
        if cam.RTSPURL == "" {
            return fmt.Errorf("camera %q: rtsp_url is required", cam.ID)
        }
        if cam.PortOffset <= 0 {
            return fmt.Errorf("camera %q: port_offset must be > 0", cam.ID)
        }
    }
    return nil
}





// package config

// import (
//     "fmt"
//     "os"

//     "gopkg.in/yaml.v3"
// )

// type ServerConfig struct {
//     Host        string `yaml:"host"`
//     BasePort    int    `yaml:"base_port"`
//     HTTPPort    int    `yaml:"http_port"`
//     OutputDir   string `yaml:"output_dir"`
//     ChunkTime   int    `yaml:"chunk_time"`
//     Encoder     string `yaml:"encoder"`
//     FrameDetail bool   `yaml:"frame_detail"`
// }

// type CameraConfig struct {
//     ID         string `yaml:"id"`
//     Name       string `yaml:"name"`
//     RTSPURL    string `yaml:"rtsp_url"`
//     PortOffset int    `yaml:"port_offset"`
//     FPS        int    `yaml:"fps"`
//     Bitrate    int    `yaml:"bitrate"`
//     Width      int    `yaml:"width"`
//     Height     int    `yaml:"height"`
//     Preset     string `yaml:"preset"`
//     Tune       string `yaml:"tune"`
//     KeyInt     int    `yaml:"keyint"`
//     BFrames    int    `yaml:"bframes"`
//     Enabled    bool   `yaml:"enabled"`
// }

// type RabbitMQConfig struct {
//     Enabled  bool   `yaml:"enabled"`
//     Host     string `yaml:"host"`
//     Port     int    `yaml:"port"`
//     User     string `yaml:"user"`
//     Password string `yaml:"password"`
//     Exchange string `yaml:"exchange"`
// }

// type Config struct {
//     Server   ServerConfig   `yaml:"server"`
//     Cameras  []CameraConfig `yaml:"cameras"`
//     RabbitMQ RabbitMQConfig `yaml:"rabbitmq"`
// }

// func Load(path string) (*Config, error) {
//     data, err := os.ReadFile(path)
//     if err != nil {
//         return nil, fmt.Errorf("cannot read config file %q: %w", path, err)
//     }
//     var cfg Config
//     if err := yaml.Unmarshal(data, &cfg); err != nil {
//         return nil, fmt.Errorf("cannot parse config file: %w", err)
//     }
//     applyDefaults(&cfg)
//     if err := validate(&cfg); err != nil {
//         return nil, err
//     }
//     return &cfg, nil
// }

// func (c *Config) EnabledCameras() []CameraConfig {
//     var out []CameraConfig
//     for _, cam := range c.Cameras {
//         if cam.Enabled {
//             out = append(out, cam)
//         }
//     }
//     return out
// }

// func (c *Config) SRTPort(cam CameraConfig) int {
//     return c.Server.BasePort + cam.PortOffset
// }

// func (c *Config) SRTCallerURI(cam CameraConfig) string {
//     return fmt.Sprintf("srt://%s:%d?mode=caller&latency=200&timeout=10000000&transtype=live&streamid=%s",
//         c.Server.Host, c.SRTPort(cam), cam.ID)
// }

// func (c *Config) SRTListenerURI(cam CameraConfig) string {
//     return fmt.Sprintf("srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live",
//         c.SRTPort(cam))
// }

// func (c *Config) CameraOutputDir(cam CameraConfig) string {
//     return fmt.Sprintf("%s/%s", c.Server.OutputDir, cam.ID)
// }

// func applyDefaults(cfg *Config) {
//     if cfg.Server.BasePort == 0 {
//         cfg.Server.BasePort = 9000
//     }
//     if cfg.Server.HTTPPort == 0 {
//         cfg.Server.HTTPPort = 8080
//     }
//     if cfg.Server.ChunkTime == 0 {
//         cfg.Server.ChunkTime = 120
//     }
//     if cfg.Server.Encoder == "" {
//         cfg.Server.Encoder = "h265"
//     }
//     if cfg.Server.OutputDir == "" {
//         cfg.Server.OutputDir = "./recordings"
//     }
//     for i := range cfg.Cameras {
//         cam := &cfg.Cameras[i]
//         if cam.FPS == 0 {
//             cam.FPS = 25
//         }
//         if cam.Bitrate == 0 {
//             cam.Bitrate = 900
//         }
//         if cam.Width == 0 {
//             cam.Width = 1920
//         }
//         if cam.Height == 0 {
//             cam.Height = 1080
//         }
//         if cam.Preset == "" {
//             cam.Preset = "ultrafast"
//         }
//         if cam.Tune == "" {
//             cam.Tune = "zerolatency"
//         }
//         if cam.KeyInt == 0 {
//             cam.KeyInt = 30
//         }
//     }
//     if cfg.RabbitMQ.Exchange == "" {
//         cfg.RabbitMQ.Exchange = "camera_events"
//     }
//     if cfg.RabbitMQ.Port == 0 {
//         cfg.RabbitMQ.Port = 5672
//     }
//     if cfg.RabbitMQ.Host == "" {
//         cfg.RabbitMQ.Host = "localhost"
//     }
//     if cfg.RabbitMQ.User == "" {
//         cfg.RabbitMQ.User = "guest"
//     }
//     if cfg.RabbitMQ.Password == "" {
//         cfg.RabbitMQ.Password = "guest"
//     }
// }

// func validate(cfg *Config) error {
//     if cfg.Server.Host == "" {
//         return fmt.Errorf("server.host is required in config.yaml")
//     }
//     for i, cam := range cfg.Cameras {
//         if cam.ID == "" {
//             return fmt.Errorf("cameras[%d]: id is required", i)
//         }
//         if cam.RTSPURL == "" {
//             return fmt.Errorf("camera %q: rtsp_url is required", cam.ID)
//         }
//         if cam.PortOffset <= 0 {
//             return fmt.Errorf("camera %q: port_offset must be > 0", cam.ID)
//         }
//     }
//     return nil
// }





// package config

// import (
// 	"fmt"
// 	"os"

// 	"gopkg.in/yaml.v3"
// )

// // ServerConfig holds settings that apply to the recording server side.
// type ServerConfig struct {
// 	Host        string `yaml:"host"`
// 	BasePort    int    `yaml:"base_port"`
// 	HTTPPort    int    `yaml:"http_port"`
// 	OutputDir   string `yaml:"output_dir"`
// 	ChunkTime   int    `yaml:"chunk_time"`
// 	Encoder     string `yaml:"encoder"`
// 	FrameDetail bool   `yaml:"frame_detail"`
// }

// // CameraConfig holds per-camera settings.
// type CameraConfig struct {
// 	ID      string `yaml:"id"`
// 	Name    string `yaml:"name"`
// 	RTSPURL string `yaml:"rtsp_url"`

// 	PortOffset int `yaml:"port_offset"`

// 	FPS     int `yaml:"fps"`
// 	Bitrate int `yaml:"bitrate"`
// 	Width   int `yaml:"width"`
// 	Height  int `yaml:"height"`

// 	Preset  string `yaml:"preset"`
// 	Tune    string `yaml:"tune"`
// 	KeyInt  int    `yaml:"keyint"`
// 	BFrames int    `yaml:"bframes"`

// 	Enabled bool `yaml:"enabled"`
// }

// // Config is the root structure of config.yaml.
// type Config struct {
// 	Server  ServerConfig   `yaml:"server"`
// 	Cameras []CameraConfig `yaml:"cameras"`
// }

// // Load reads and parses config.yaml from path.
// func Load(path string) (*Config, error) {
// 	data, err := os.ReadFile(path)
// 	if err != nil {
// 		return nil, fmt.Errorf("cannot read config file %q: %w", path, err)
// 	}
// 	var cfg Config
// 	if err := yaml.Unmarshal(data, &cfg); err != nil {
// 		return nil, fmt.Errorf("cannot parse config file: %w", err)
// 	}
// 	applyDefaults(&cfg)
// 	if err := validate(&cfg); err != nil {
// 		return nil, err
// 	}
// 	return &cfg, nil
// }

// // EnabledCameras returns cameras that have enabled: true.
// func (c *Config) EnabledCameras() []CameraConfig {
// 	var out []CameraConfig
// 	for _, cam := range c.Cameras {
// 		if cam.Enabled {
// 			out = append(out, cam)
// 		}
// 	}
// 	return out
// }

// // SRTPort returns the SRT port for a camera (base_port + port_offset).
// func (c *Config) SRTPort(cam CameraConfig) int {
// 	return c.Server.BasePort + cam.PortOffset
// }

// // SRTCallerURI returns the SRT URI the local sender should connect to.
// func (c *Config) SRTCallerURI(cam CameraConfig) string {
// 	return fmt.Sprintf("srt://%s:%d?mode=caller&latency=200&timeout=10000000", c.Server.Host, c.SRTPort(cam))
// }

// // SRTListenerURI returns the SRT URI the server receiver should listen on.
// func (c *Config) SRTListenerURI(cam CameraConfig) string {
// 	return fmt.Sprintf("srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live", c.SRTPort(cam))
// }

// // CameraOutputDir returns the recording directory for a specific camera.
// func (c *Config) CameraOutputDir(cam CameraConfig) string {
// 	return fmt.Sprintf("%s/%s", c.Server.OutputDir, cam.ID)
// }

// func applyDefaults(cfg *Config) {
// 	if cfg.Server.BasePort == 0 {
// 		cfg.Server.BasePort = 9000
// 	}
// 	if cfg.Server.HTTPPort == 0 {
// 		cfg.Server.HTTPPort = 8080
// 	}
// 	if cfg.Server.ChunkTime == 0 {
// 		cfg.Server.ChunkTime = 120
// 	}
// 	if cfg.Server.Encoder == "" {
// 		cfg.Server.Encoder = "h265"
// 	}
// 	if cfg.Server.OutputDir == "" {
// 		cfg.Server.OutputDir = "./recordings"
// 	}
// 	for i := range cfg.Cameras {
// 		cam := &cfg.Cameras[i]
// 		if cam.FPS == 0 {
// 			cam.FPS = 25
// 		}
// 		if cam.Bitrate == 0 {
// 			cam.Bitrate = 900
// 		}
// 		if cam.Width == 0 {
// 			cam.Width = 1920
// 		}
// 		if cam.Height == 0 {
// 			cam.Height = 1080
// 		}
// 		if cam.Preset == "" {
// 			cam.Preset = "ultrafast"
// 		}
// 		if cam.Tune == "" {
// 			cam.Tune = "zerolatency"
// 		}
// 		if cam.KeyInt == 0 {
// 			cam.KeyInt = 30
// 		}
// 	}
// }

// func validate(cfg *Config) error {
// 	if cfg.Server.Host == "" {
// 		return fmt.Errorf("server.host is required in config.yaml")
// 	}
// 	for i, cam := range cfg.Cameras {
// 		if cam.ID == "" {
// 			return fmt.Errorf("cameras[%d]: id is required", i)
// 		}
// 		if cam.RTSPURL == "" {
// 			return fmt.Errorf("camera %q: rtsp_url is required", cam.ID)
// 		}
// 		if cam.PortOffset <= 0 {
// 			return fmt.Errorf("camera %q: port_offset must be > 0", cam.ID)
// 		}
// 	}
// 	return nil
// }
