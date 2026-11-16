package mq

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "time"

    "github.com/streadway/amqp"
)

var Debug = true // set to false to reduce noise

type Client struct {
    conn     *amqp.Connection
    channel  *amqp.Channel
    exchange string
}

type Message struct {
    CameraID  string      `json:"camera_id"`
    Type      string      `json:"type"`
    Timestamp time.Time   `json:"timestamp"`
    Data      interface{} `json:"data"`
}

type StatusData struct {
    Status  string `json:"status"`
    FPS     int    `json:"fps,omitempty"`
    Bitrate int    `json:"bitrate,omitempty"`
}

type ChunkData struct {
    ChunkNumber    int     `json:"chunk_number"`
    File           string  `json:"file"`
    SizeMB         float64 `json:"size_mb"`
    DurationSec    float64 `json:"duration_sec"`
    AvgBitrateKbps float64 `json:"avg_bitrate_kbps"`
    FrameCount     int     `json:"frame_count"`
}

type Command struct {
    Command string          `json:"command"`
    Params  json.RawMessage `json:"params,omitempty"`
}

func NewClient(host string, port int, user, password, exchange string) (*Client, error) {
    url := fmt.Sprintf("amqp://%s:%s@%s:%d/", user, password, host, port)
    logf("[MQ] Connecting to RabbitMQ at %s:%d ...", host, port)
    conn, err := amqp.Dial(url)
    if err != nil {
        logf("[MQ] ERROR: connection failed: %v", err)
        return nil, err
    }
    logf("[MQ] Connected successfully")

    ch, err := conn.Channel()
    if err != nil {
        conn.Close()
        logf("[MQ] ERROR: failed to open channel: %v", err)
        return nil, err
    }

    err = ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil)
    if err != nil {
        ch.Close()
        conn.Close()
        logf("[MQ] ERROR: failed to declare exchange %q: %v", exchange, err)
        return nil, err
    }
    logf("[MQ] Exchange %q declared", exchange)

    return &Client{conn: conn, channel: ch, exchange: exchange}, nil
}

func (c *Client) Publish(routingKey string, body []byte) error {
    if Debug {
        logf("[MQ] PUBLISH %s -> %s", routingKey, string(body))
    }
    err := c.channel.Publish(c.exchange, routingKey, false, false,
        amqp.Publishing{ContentType: "application/json", Body: body})
    if err != nil {
        logf("[MQ] ERROR publishing to %s: %v", routingKey, err)
    }
    return err
}

func (c *Client) PublishCommand(cameraID, command string, params json.RawMessage) error {
    cmd := Command{Command: command, Params: params}
    body, err := json.Marshal(cmd)
    if err != nil {
        logf("[MQ] ERROR marshalling command: %v", err)
        return err
    }
    routingKey := "camera." + cameraID + ".command"
    logf("[MQ] PUBLISH command camera=%s command=%s", cameraID, command)
    return c.channel.Publish(c.exchange, routingKey, true, false,
        amqp.Publishing{ContentType: "application/json", Body: body, DeliveryMode: amqp.Persistent})
}

func (c *Client) PublishStatus(cameraID, status string, fps, bitrate int) error {
    data := StatusData{Status: status, FPS: fps, Bitrate: bitrate}
    logf("[MQ] PUBLISH status camera=%s status=%s fps=%d bitrate=%d", cameraID, status, fps, bitrate)
    return c.publishMessage("camera."+cameraID+".status", data)
}

func (c *Client) PublishChunk(cameraID string, chunk ChunkData) error {
    logf("[MQ] PUBLISH chunk camera=%s chunk=%d size=%.2fMB", cameraID, chunk.ChunkNumber, chunk.SizeMB)
    return c.publishMessage("camera."+cameraID+".chunk", chunk)
}

func (c *Client) publishMessage(routingKey string, data interface{}) error {
    msg := Message{
        CameraID:  extractCameraID(routingKey),
        Type:      extractType(routingKey),
        Timestamp: time.Now(),
        Data:      data,
    }
    body, err := json.Marshal(msg)
    if err != nil {
        logf("[MQ] ERROR marshalling message: %v", err)
        return err
    }
    return c.Publish(routingKey, body)
}

func (c *Client) ConsumeWithContext(ctx context.Context, routingKey string, handler func([]byte)) error {
    q, err := c.channel.QueueDeclare("", false, true, true, false, nil)
    if err != nil {
        logf("[MQ] ERROR declaring queue for %s: %v", routingKey, err)
        return err
    }
    logf("[MQ] CONSUME binding queue %s to %s", q.Name, routingKey)
    err = c.channel.QueueBind(q.Name, routingKey, c.exchange, false, nil)
    if err != nil {
        logf("[MQ] ERROR binding queue: %v", err)
        return err
    }
    msgs, err := c.channel.Consume(q.Name, "", true, false, false, false, nil)
    if err != nil {
        logf("[MQ] ERROR starting consume: %v", err)
        return err
    }
    go func() {
        for {
            select {
            case <-ctx.Done():
                logf("[MQ] Stopping consumer for %s", routingKey)
                c.channel.Cancel(q.Name, false)
                return
            case d, ok := <-msgs:
                if !ok {
                    logf("[MQ] Consumer channel closed for %s", routingKey)
                    return
                }
                logf("[MQ] RECEIVED %s -> %s", d.RoutingKey, string(d.Body))
                handler(d.Body)
            }
        }
    }()
    return nil
}

func (c *Client) StartHeartbeat(ctx context.Context, cameraID string, fps, bitrate int) {
    ticker := time.NewTicker(15 * time.Second)
    defer ticker.Stop()
    logf("[MQ] Heartbeat started for camera %s (interval 15s)", cameraID)
    for {
        select {
        case <-ctx.Done():
            logf("[MQ] Heartbeat stopped for camera %s", cameraID)
            return
        case <-ticker.C:
            logf("[MQ] HEARTBEAT sending for camera %s", cameraID)
            _ = c.PublishStatus(cameraID, "heartbeat", fps, bitrate)
        }
    }
}

func (c *Client) Close() {
    logf("[MQ] Closing connection...")
    if c.channel != nil {
        c.channel.Close()
    }
    if c.conn != nil {
        c.conn.Close()
    }
    logf("[MQ] Connection closed")
}

func extractCameraID(routingKey string) string {
    parts := splitRoutingKey(routingKey)
    if len(parts) >= 2 {
        return parts[1]
    }
    return ""
}

func extractType(routingKey string) string {
    parts := splitRoutingKey(routingKey)
    if len(parts) >= 3 {
        return parts[2]
    }
    return ""
}

func splitRoutingKey(routingKey string) []string {
    var parts []string
    start := 0
    for i, ch := range routingKey {
        if ch == '.' {
            parts = append(parts, routingKey[start:i])
            start = i + 1
        }
    }
    parts = append(parts, routingKey[start:])
    return parts
}

func logf(format string, v ...interface{}) {
    log.Printf("[%s] "+format, append([]interface{}{time.Now().Format("15:04:05.000")}, v...)...)
}



// package mq

// import (
//     "encoding/json"
//     "fmt"
//     "time"

//     "github.com/streadway/amqp"
// )

// type Client struct {
//     conn     *amqp.Connection
//     channel  *amqp.Channel
//     exchange string
// }

// type Message struct {
//     CameraID  string      `json:"camera_id"`
//     Type      string      `json:"type"`
//     Timestamp time.Time   `json:"timestamp"`
//     Data      interface{} `json:"data"`
// }

// type StatusData struct {
//     Status  string `json:"status"`
//     FPS     int    `json:"fps,omitempty"`
//     Bitrate int    `json:"bitrate,omitempty"`
// }

// type ChunkData struct {
//     ChunkNumber    int     `json:"chunk_number"`
//     File           string  `json:"file"`
//     SizeMB         float64 `json:"size_mb"`
//     DurationSec    float64 `json:"duration_sec"`
//     AvgBitrateKbps float64 `json:"avg_bitrate_kbps"`
//     FrameCount     int     `json:"frame_count"`
// }

// type Command struct {
//     Command string          `json:"command"`
//     Params  json.RawMessage `json:"params,omitempty"`
// }

// func NewClient(host string, port int, user, password, exchange string) (*Client, error) {
//     url := fmt.Sprintf("amqp://%s:%s@%s:%d/", user, password, host, port)
//     conn, err := amqp.Dial(url)
//     if err != nil {
//         return nil, err
//     }
//     ch, err := conn.Channel()
//     if err != nil {
//         conn.Close()
//         return nil, err
//     }
//     err = ch.ExchangeDeclare(
//         exchange, // name
//         "topic",  // type
//         true,     // durable
//         false,    // auto-deleted
//         false,    // internal
//         false,    // no-wait
//         nil,      // arguments
//     )
//     if err != nil {
//         ch.Close()
//         conn.Close()
//         return nil, err
//     }
//     return &Client{
//         conn:     conn,
//         channel:  ch,
//         exchange: exchange,
//     }, nil
// }

// func (c *Client) Publish(routingKey string, body []byte) error {
//     return c.channel.Publish(
//         c.exchange,
//         routingKey,
//         false, // mandatory
//         false, // immediate
//         amqp.Publishing{
//             ContentType: "application/json",
//             Body:        body,
//         },
//     )
// }

// func (c *Client) PublishStatus(cameraID, status string, fps, bitrate int) error {
//     data := StatusData{Status: status, FPS: fps, Bitrate: bitrate}
//     return c.publishMessage("camera."+cameraID+".status", data)
// }

// func (c *Client) PublishChunk(cameraID string, chunk ChunkData) error {
//     return c.publishMessage("camera."+cameraID+".chunk", chunk)
// }

// func (c *Client) publishMessage(routingKey string, data interface{}) error {
//     msg := Message{
//         CameraID:  extractCameraID(routingKey),
//         Type:      extractType(routingKey),
//         Timestamp: time.Now(),
//         Data:      data,
//     }
//     body, err := json.Marshal(msg)
//     if err != nil {
//         return err
//     }
//     return c.Publish(routingKey, body)
// }

// func (c *Client) Consume(routingKey string, handler func([]byte)) error {
//     q, err := c.channel.QueueDeclare(
//         "",    // name – server generates a random name
//         false, // durable
//         true,  // auto-delete
//         true,  // exclusive
//         false, // no-wait
//         nil,   // args
//     )
//     if err != nil {
//         return err
//     }
//     err = c.channel.QueueBind(q.Name, routingKey, c.exchange, false, nil)
//     if err != nil {
//         return err
//     }
//     msgs, err := c.channel.Consume(q.Name, "", true, false, false, false, nil)
//     if err != nil {
//         return err
//     }
//     go func() {
//         for d := range msgs {
//             handler(d.Body)
//         }
//     }()
//     return nil
// }

// func (c *Client) Close() {
//     if c.channel != nil {
//         c.channel.Close()
//     }
//     if c.conn != nil {
//         c.conn.Close()
//     }
// }

// // helpers
// func extractCameraID(routingKey string) string {
//     parts := splitRoutingKey(routingKey)
//     if len(parts) >= 2 {
//         return parts[1]
//     }
//     return ""
// }
// func extractType(routingKey string) string {
//     parts := splitRoutingKey(routingKey)
//     if len(parts) >= 3 {
//         return parts[2]
//     }
//     return ""
// }
// func splitRoutingKey(routingKey string) []string {
//     // simple split; more robust would handle escaped dots, but not needed here
//     var parts []string
//     start := 0
//     for i, ch := range routingKey {
//         if ch == '.' {
//             parts = append(parts, routingKey[start:i])
//             start = i + 1
//         }
//     }
//     parts = append(parts, routingKey[start:])
//     return parts
// }