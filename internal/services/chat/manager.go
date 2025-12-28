/*
package chat

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"src-engine-v2/internal/config"
	"sync"
)

type Manager struct {
	activeConn net.Conn
	mu         sync.Mutex
	
	// Mesaj geldiğinde tetiklenecek fonksiyon (UI'a iletmek için)
	onMessage func(string)
}

func NewManager() *Manager {
	return &Manager{}
}

// SetCallback: Gelen mesajı yakalamak için
func (m *Manager) SetCallback(cb func(string)) {
	m.onMessage = cb
}

// Start: 9004 portunu dinler
func (m *Manager) Start(ln net.Listener) {
	fmt.Printf("💬 Chat Service Ready (Port: %d)\n", config.PortChat)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		m.mu.Lock()
		if m.activeConn != nil {
			conn.Close()
			m.mu.Unlock()
			continue
		}
		m.activeConn = conn
		m.mu.Unlock()

		fmt.Println("💬 Chat connection established..")
		go m.readLoop(conn)
	}
}

// Send: Karşı tarafa mesaj gönderir
func (m *Manager) Send(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activeConn == nil {
		return fmt.Errorf("No connection")
	}

	data := []byte(text)
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(data)))

	// Header Yaz
	if _, err := m.activeConn.Write(header); err != nil {
		return err
	}
	// Mesaj Yaz
	if _, err := m.activeConn.Write(data); err != nil {
		return err
	}

	return nil
}

func (m *Manager) readLoop(conn net.Conn) {
	defer func() {
		m.mu.Lock()
		if m.activeConn != nil {
			m.activeConn.Close()
			m.activeConn = nil
		}
		m.mu.Unlock()
		fmt.Println("💬 Chat connection lost.")
	}()

	header := make([]byte, 4)

	for {
		// 1. Uzunluk Oku
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		length := binary.LittleEndian.Uint32(header)
		if length > 1024*10 { // Max 10KB mesaj (Spam koruması)
			return
		}

		// 2. Metni Oku
		msgBuf := make([]byte, length)
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return
		}

		text := string(msgBuf)
		
		// Logla veya UI'a ilet
		fmt.Printf("📩 Incoming Message: %s\n", text)
		
		if m.onMessage != nil {
			m.onMessage(text)
		}
	}
}
	*/

package chat

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"src-engine-v2/internal/config"
	"sync"
)

type Manager struct {
	activeConn net.Conn
	mu         sync.Mutex
	
	// Mesaj geldiğinde tetiklenecek fonksiyon (UI'a iletmek için)
	onMessage func(string)
}

func NewManager() *Manager {
	return &Manager{}
}

// SetCallback: Gelen mesajı yakalamak için
func (m *Manager) SetCallback(cb func(string)) {
	m.onMessage = cb
}

// Start: VPN üzerindeki dinleyici (Dış Dünya - Viewer ile konuşur)
func (m *Manager) Start(ln net.Listener) {
	fmt.Printf("💬 Chat Service Ready (VPN Port: %d)\n", config.PortChat)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		m.mu.Lock()
		if m.activeConn != nil {
			conn.Close()
			m.mu.Unlock()
			continue
		}
		m.activeConn = conn
		m.mu.Unlock()

		fmt.Println("💬 Chat connection established (VPN).")
		go m.readLoop(conn)
	}
}

// 🔥 YENİ: StartLocal (Electron -> Go İletişimi İçin)
// Dashboard, "AUTH_RESPONSE:OK" mesajını buraya gönderir, biz de karşıya iletiriz.
func (m *Manager) StartLocal(port int) {
	// Sadece localhost'u dinle
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Printf("⚠️ Local Chat Port Failed: %v\n", err)
		return
	}
	fmt.Printf("💬 Local Chat Bridge Ready (Local Port: %d)\n", port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		
		// Electron bağlanıp bir şey gönderdiğinde:
		go func(c net.Conn) {
			defer c.Close()
			
			// Veriyi oku (Electron zaten header + data formatında gönderiyor)
			data, err := io.ReadAll(c)
			if err != nil || len(data) == 0 {
				return
			}

			// Eğer karşı tarafa (Viewer) bağlıysak, veriyi olduğu gibi ilet (Relay)
			m.mu.Lock()
			if m.activeConn != nil {
				_, _ = m.activeConn.Write(data)
				// fmt.Println("💬 Command relayed from Electron to Remote.")
			}
			m.mu.Unlock()
		}(conn)
	}
}

// Send: Go içinden mesaj göndermek için (Pano vs.)
func (m *Manager) Send(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activeConn == nil {
		return fmt.Errorf("No connection")
	}

	data := []byte(text)
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(data)))

	// Header Yaz
	if _, err := m.activeConn.Write(header); err != nil {
		return err
	}
	// Mesaj Yaz
	if _, err := m.activeConn.Write(data); err != nil {
		return err
	}

	return nil
}

func (m *Manager) readLoop(conn net.Conn) {
	defer func() {
		m.mu.Lock()
		if m.activeConn != nil {
			m.activeConn.Close()
			m.activeConn = nil
		}
		m.mu.Unlock()
		fmt.Println("💬 Chat connection lost.")
	}()

	header := make([]byte, 4)

	for {
		// 1. Uzunluk Oku
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		length := binary.LittleEndian.Uint32(header)
		if length > 1024*50 { // Max 50KB (Clipboard için artırdık)
			return
		}

		// 2. Metni Oku
		msgBuf := make([]byte, length)
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return
		}

		text := string(msgBuf)
		
		// Logla veya UI'a ilet
		// fmt.Printf("📩 Incoming Message: %s\n", text)
		
		if m.onMessage != nil {
			m.onMessage(text)
		}
	}
}