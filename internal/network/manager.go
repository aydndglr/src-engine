/*
package network

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"src-engine-v2/internal/config"
	"time"

	"tailscale.com/tsnet"
)

// Manager: Headscale/Tailscale bağlantısını ve port yönetimini sağlar.
type Manager struct {
	Server *tsnet.Server
	Conf   *config.Config
	MyIP   string
}

// NewManager: Yeni bir ağ yöneticisi oluşturur.
func NewManager(cfg *config.Config) *Manager {
	// Durum dosyaları için klasör yolu (~/.src-engine/hostname)
	homeDir, _ := os.UserHomeDir()
	if cfg.Network.DataDir == "" {
		cfg.Network.DataDir = filepath.Join(homeDir, ".src-engine", cfg.Network.Hostname)
	}
	_ = os.MkdirAll(cfg.Network.DataDir, 0700)

	s := &tsnet.Server{
		Hostname:   cfg.Network.Hostname,
		AuthKey:    cfg.AuthKey,
		ControlURL: cfg.Network.ControlURL,
		Dir:        cfg.Network.DataDir,
		Logf: func(format string, args ...any) {
			if cfg.Network.LogEnabled {
				//log.Printf("[TSNET] "+format, args...)
			}
		},
	}

	return &Manager{
		Server: s,
		Conf:   cfg,
	}
}

// Start: VPN ağına bağlanır ve hazır olana kadar bekler.
func (m *Manager) Start(ctx context.Context) error {
	// Motoru tetiklemek için sahte bir dinleyici açıp kapatıyoruz (Kickstart)
	ln, err := m.Server.Listen("tcp", ":0")
	if err == nil {
		ln.Close()
	}

	lc, err := m.Server.LocalClient()
	if err != nil {
		return fmt.Errorf("local client error: %v", err)
	}

	fmt.Println("⏳ Connecting to VPN Network...")

	// Hazır Olana Kadar Bekle (Timeout config'den gelir)
	timeoutCtx, cancel := context.WithTimeout(ctx, config.ConnectTimeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("Timeout: VPN connection could not be established.")
		case <-ticker.C:
			st, err := lc.Status(ctx)
			if err != nil {
				continue
			}

			// BackendState "Running" olmalı
			if st.BackendState == "Running" {
				for _, ip := range st.TailscaleIPs {
					if ip.Is4() {
						m.MyIP = ip.String()
						fmt.Printf("✅ VPN Tunnel Established! IP: %s\n", m.MyIP)
						return nil
					}
				}
			}
		}
	}
}

// Listen: Belirtilen portu dinlemeye başlar (Sunucu Modu).
func (m *Manager) Listen(port int) (net.Listener, error) {
	return m.Server.Listen("tcp", fmt.Sprintf(":%d", port))
}

// Dial: Hedef IP ve Porta bağlanır (İstemci Modu).
func (m *Manager) Dial(ctx context.Context, targetIP string, port int) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, config.ConnectTimeout)
	defer cancel()

	conn, err := m.Server.Dial(dialCtx, "tcp", fmt.Sprintf("%s:%d", targetIP, port))
	if err != nil {
		return nil, err
	}

	// 🔥 NETWORK BOOST: Tampon Ayarları
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(config.KeepAlive)
		
		// 1 MB Tampon (Veri şişmesini önler)
		_ = tcpConn.SetWriteBuffer(128 * 1024)
		_ = tcpConn.SetReadBuffer(128 * 1024)
		_ = tcpConn.SetNoDelay(true)
	}

	return conn, nil
}

// ListenTCP: Engine.go uyumluluğu için (Listen fonksiyonunu çağırır)
func (m *Manager) ListenTCP(port int) (net.Listener, error) {
	return m.Listen(port)
}

// DialTCP: Engine.go uyumluluğu için (Context yönetimi ile Dial fonksiyonunu çağırır)
func (m *Manager) DialTCP(targetIP string, port int) (net.Conn, error) {
	// Engine context göndermediği için varsayılan Background context ile çağırıyoruz
	return m.Dial(context.Background(), targetIP, port)
}

*/

package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"src-engine-v2/internal/config"
	"time"

	"tailscale.com/tsnet"
)

// Manager: Headscale/Tailscale bağlantısını ve port yönetimini sağlar.
type Manager struct {
	Server *tsnet.Server
	Conf   *config.Config
	MyIP   string
}

// NewManager: Yeni bir ağ yöneticisi oluşturur.
func NewManager(cfg *config.Config) *Manager {
	// Durum dosyaları için klasör yolu (~/.src-engine/hostname)
	homeDir, _ := os.UserHomeDir()
	if cfg.Network.DataDir == "" {
		cfg.Network.DataDir = filepath.Join(homeDir, ".src-engine", cfg.Network.Hostname)
	}
	_ = os.MkdirAll(cfg.Network.DataDir, 0700)

	s := &tsnet.Server{
		Hostname:   cfg.Network.Hostname,
		AuthKey:    cfg.AuthKey,
		ControlURL: cfg.Network.ControlURL,
		Dir:        cfg.Network.DataDir,
		Logf: func(format string, args ...any) {
			if cfg.Network.LogEnabled {
				// Tailscale loglarını şimdilik kapalı tutuyoruz, çok gürültü yapmasın
				// log.Printf("[TSNET] "+format, args...)
			}
		},
	}

	return &Manager{
		Server: s,
		Conf:   cfg,
	}
}

// Start: VPN ağına bağlanır ve hazır olana kadar bekler.
func (m *Manager) Start(ctx context.Context) error {
	// Motoru tetiklemek için sahte bir dinleyici açıp kapatıyoruz (Kickstart)
	ln, err := m.Server.Listen("tcp", ":0")
	if err == nil {
		ln.Close()
	}

	lc, err := m.Server.LocalClient()
	if err != nil {
		return fmt.Errorf("local client error: %v", err)
	}

	fmt.Println("⏳ Connecting to VPN Network...")

	// Hazır Olana Kadar Bekle (Timeout config'den gelir)
	timeoutCtx, cancel := context.WithTimeout(ctx, config.ConnectTimeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("Timeout: VPN connection could not be established.")
		case <-ticker.C:
			st, err := lc.Status(ctx)
			if err != nil {
				continue
			}

			// BackendState "Running" olmalı
			if st.BackendState == "Running" {
				for _, ip := range st.TailscaleIPs {
					if ip.Is4() {
						m.MyIP = ip.String()
						fmt.Printf("✅ VPN Tunnel Established! IP: %s\n", m.MyIP)
						return nil
					}
				}
			}
		}
	}
}

// --- 🔥 AUTH LISTENER (GÜVENLİK SARMALAYICISI - LOGLU VE DÖNGÜSEL) ---
// Gelen bağlantıları süzgeçten geçirir. Şifre yanlışsa anında koparır.

type AuthListener struct {
	net.Listener
	password string
	port     int // Hata ayıklama için port bilgisini tutuyoruz
}

func (l *AuthListener) Accept() (net.Conn, error) {
	// Sonsuz döngü: Hatalı bağlantıları eleyip yenisini beklemek için
	for {
		// 1. Fiziksel Bağlantıyı Kabul Et
		conn, err := l.Listener.Accept()
		if err != nil {
			// Listener'ın kendisi hata verdiyse (kapatıldıysa vs) dön
			return nil, err
		}

		// Eğer şifre yoksa direkt kabul et (Şifresiz Mod)
		if l.password == "" {
			return conn, nil
		}

		// 2. Handshake (El Sıkışma) Süreci
		// 3 saniye içinde şifre paketini göndermezse bağlantıyı kes (DDoS/Lag koruması)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		
		buf := make([]byte, 128) // Şifre için yeterli alan
		n, err := conn.Read(buf)
		
		// Timeout'u kaldır (Bundan sonra normal akışa dönsün)
		conn.SetReadDeadline(time.Time{})

		if err != nil {
			// Okuma hatası (Muhtemelen karşı taraf veri göndermeden kapattı veya timeout)
			// Hata bas ama fonksiyonu bitirme, döngüye devam et (continue)
			if err != io.EOF {
				fmt.Printf("⛔ Auth Handshake Read Error (Port %d): %v\n", l.port, err)
			}
			conn.Close()
			continue 
		}

		// Gelen paket "AUTH:şifre" formatında mı?
		received := string(buf[:n])
		expected := "AUTH:" + l.password

		if received != expected {
			fmt.Printf("⛔ Auth Failed! Wrong Password on Port %d. (Got: %s)\n", l.port, received)
			conn.Close()
			continue
		}

		// Başarılı!
		// fmt.Printf("🔓 Auth Successful on Port %d from %s\n", l.port, conn.RemoteAddr())
		return conn, nil
	}
}

// Listen: Belirtilen portu dinlemeye başlar (Sunucu Modu).
// 🔥 GÜNCELLENDİ: AuthListener kullanıyor ve Port bilgisini iletiyor.
func (m *Manager) Listen(port int) (net.Listener, error) {
	ln, err := m.Server.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	
	// Sarmalayıcıyı (Wrapper) döndür
	return &AuthListener{
		Listener: ln, 
		password: m.Conf.SessionPassword,
		port:     port,
	}, nil
}

// Dial: Hedef IP ve Porta bağlanır (İstemci Modu).
// 🔥 GÜNCELLENDİ: Bağlanır bağlanmaz şifreyi gönderiyor.
func (m *Manager) Dial(ctx context.Context, targetIP string, port int) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, config.ConnectTimeout)
	defer cancel()

	conn, err := m.Server.Dial(dialCtx, "tcp", fmt.Sprintf("%s:%d", targetIP, port))
	if err != nil {
		return nil, err
	}

	// 🔥 Şifre Varsa Gönder (Handshake)
	if m.Conf.SessionPassword != "" {
		authPacket := "AUTH:" + m.Conf.SessionPassword
		_, err := conn.Write([]byte(authPacket))
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("auth send failed: %v", err)
		}
		// fmt.Printf("📤 Auth Packet Sent to %s:%d\n", targetIP, port)
	}

	// 🔥 NETWORK BOOST: Tampon Ayarları
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(config.KeepAlive)
		
		// 1 MB Tampon (Veri şişmesini önler)
		_ = tcpConn.SetWriteBuffer(128 * 1024)
		_ = tcpConn.SetReadBuffer(128 * 1024)
		_ = tcpConn.SetNoDelay(true)
	}

	return conn, nil
}

// ListenTCP: Engine.go uyumluluğu için
func (m *Manager) ListenTCP(port int) (net.Listener, error) {
	return m.Listen(port)
}

// DialTCP: Engine.go uyumluluğu için
func (m *Manager) DialTCP(targetIP string, port int) (net.Conn, error) {
	return m.Dial(context.Background(), targetIP, port)
}