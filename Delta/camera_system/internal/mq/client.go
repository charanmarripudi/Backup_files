package mq

import (
    "encoding/json"
    "fmt"
    "time"

    "github.com/streadway/amqp"
)

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
    conn, err := amqp.Dial(url)
    if err != nil {
        return nil, err
    }
    ch, err := conn.Channel()
    if err != nil {
        conn.Close()
        return nil, err
    }
    err = ch.ExchangeDeclare(
        exchange, // name
        "topic",  // type
        true,     // durable
        false,    // auto-deleted
        false,    // internal
        false,    // no-wait
        nil,      // arguments
    )
    if err != nil {
        ch.Close()
        conn.Close()
        return nil, err
    }
    return &Client{
        conn:     conn,
        channel:  ch,
        exchange: exchange,
    }, nil
}

func (c *Client) Publish(routingKey string, body []byte) error {
    return c.channel.Publish(
        c.exchange,
        routingKey,
        false, // mandatory
        false, // immediate
        amqp.Publishing{
            ContentType: "application/json",
            Body:        body,
        },
    )
}

func (c *Client) PublishStatus(cameraID, status string, fps, bitrate int) error {
    data := StatusData{Status: status, FPS: fps, Bitrate: bitrate}
    return c.publishMessage("camera."+cameraID+".status", data)
}

func (c *Client) PublishChunk(cameraID string, chunk ChunkData) error {
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
        return err
    }
    return c.Publish(routingKey, body)
}

func (c *Client) Consume(routingKey string, handler func([]byte)) error {
    q, err := c.channel.QueueDeclare(
        "",    // name – server generates a random name
        false, // durable
        true,  // auto-delete
        true,  // exclusive
        false, // no-wait
        nil,   // args
    )
    if err != nil {
        return err
    }
    err = c.channel.QueueBind(q.Name, routingKey, c.exchange, false, nil)
    if err != nil {
        return err
    }
    msgs, err := c.channel.Consume(q.Name, "", true, false, false, false, nil)
    if err != nil {
        return err
    }
    go func() {
        for d := range msgs {
            handler(d.Body)
        }
    }()
    return nil
}

func (c *Client) Close() {
    if c.channel != nil {
        c.channel.Close()
    }
    if c.conn != nil {
        c.conn.Close()
    }
}

// helpers
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
    // simple split; more robust would handle escaped dots, but not needed here
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