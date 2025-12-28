package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"src-engine-v2/internal/config"
	"src-engine-v2/internal/network"
	"src-engine-v2/internal/services/audio"
	"src-engine-v2/internal/services/chat"
	"src-engine-v2/internal/services/clipboard" // 🔥 YENİ: Pano Servisi
	"src-engine-v2/internal/services/filetransfer"
	"src-engine-v2/internal/services/stream"
	"strings" // 🔥 YENİ: String işlemleri için
	"syscall"
	"time"
)

// Deneme Süresi (Dakika)
const TrialLimitMinutes = 300

type App struct {
	Config  *config.Config
	Network *network.Manager
	
	// Servisler
	StreamSvc    *stream.Manager
	AudioSvc     *audio.Manager
	FileSvc      *filetransfer.Manager
	ChatSvc      *chat.Manager
	ClipboardSvc *clipboard.Manager // 🔥 YENİ
}

func NewApp(cfg *config.Config) *App {
	return &App{
		Config:  cfg,
		Network: network.NewManager(cfg),
		
		StreamSvc:    stream.NewManager(cfg),
		AudioSvc:     audio.NewManager(),
		FileSvc:      filetransfer.NewManager(),
		ChatSvc:      chat.NewManager(),
		ClipboardSvc: clipboard.NewManager(), // 🔥 YENİ
	}
}

func (a *App) Run() {
	fmt.Println("🚀 SRC-Engine V2 is starting up...")

	// 1. DENEME MODU KONTROLÜ (TRIAL CHECK)
	isTrial := os.Getenv("SRC_TRIAL_MODE") == "1"
	
	if isTrial {
		fmt.Println("⏳ Free Trial Mode Active ...")
		if err := checkTrialLimit(); err != nil {
			fmt.Printf("\n🛑 TRIAL PERIOD HAS ENDED!\n   -> %v\n", err)
			fmt.Println("   -> To continue, please purchase a license key..")
			time.Sleep(5 * time.Second)
			os.Exit(1)
		}
		// Arka planda süreyi saymaya başla
		go startTrialTicker()
	}

	// 2. AĞ BAĞLANTISI (VPN & ANAHTAR DOĞRULAMA)
	fmt.Println("🔐 Network Switch Verification...")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := a.Network.Start(ctx); err != nil {
		fmt.Printf("\n🛑 CONNECTION ERROR:\n   -> %v\n", err)
		if isTrial {
			fmt.Println("   -> The free server might be busy or the key might have expired..")
		} else {
			fmt.Println("   -> Your license key is invalid or has expired..")
		}
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}

	fmt.Println("✅ Connection Successful!")

	// 3. MOD SEÇİMİ VE BAŞLATMA
	
	if a.Config.Network.ConnectIP != "" {
		// --- CLIENT MODU (İzleyici) ---
		targetIP := a.Config.Network.ConnectIP
		fmt.Printf("📺 CLIENT MODE ACTIVE -> Target: %s\n", targetIP)
		fmt.Println("   (UI pending...)")

		// 4 Kanal İçin Proxy Başlat (Localhost <-> VPN)
		go a.startProxy(config.PortStream, targetIP)
		go a.startProxy(config.PortAudio, targetIP)
		go a.startProxy(config.PortFile, targetIP)
		go a.startProxy(config.PortChat, targetIP)

	} else {
		// --- HOST MODU (Yayıncı) ---
		fmt.Println("🎥 HOST MODE ACTIVE -> Broadcast Begins...")

		// 🔥 PANO (CLIPBOARD) ENTEGRASYONU
		// Sadece Host tarafında gerçek clipboard servisini başlatıyoruz.
		if err := clipboard.Init(); err != nil {
			fmt.Println("⚠️ The control panel service could not be started.:", err)
		} else {
			// Dinleyiciyi başlat
			a.ClipboardSvc.StartWatcher(context.Background())

			// A) Host Panosu Değişince -> Chat Kanalından Client'a Yolla
			a.ClipboardSvc.SetCallback(func(text string) {
				// "CLIPBOARD:" etiketiyle gönderiyoruz ki viewer.js anlasın
				_ = a.ChatSvc.Send("CLIPBOARD:" + text)
			})

			// B) Chat Kanalından Mesaj Gelince -> Host Panosuna Yaz (Eğer CLIPBOARD etiketi varsa)
			a.ChatSvc.SetCallback(func(msg string) {
				if strings.HasPrefix(msg, "CLIPBOARD:") {
					content := strings.TrimPrefix(msg, "CLIPBOARD:")
					a.ClipboardSvc.Write(content)
					// fmt.Println("📋 Client'tan pano verisi alındı.")
				} else {
					fmt.Printf("💬 Chat: %s\n", msg)
				}
			})
			
			fmt.Println("📋 Dashboard Synchronization Active!")
		}

		go func() { a.StreamSvc.Start(mustListen(a.Network, config.PortStream)) }()
		go func() { a.AudioSvc.Start(mustListen(a.Network, config.PortAudio)) }()
		go func() { a.FileSvc.Start(mustListen(a.Network, config.PortFile)) }() // Dosya servisi zaten burada aktif
		go func() { a.ChatSvc.Start(mustListen(a.Network, config.PortChat)) }()
		
	}

	fmt.Println("✅ SYSTEM ACTIVE! (Close with CTRL+C)")

	// 4. KAPANIŞ SİNYALİNİ BEKLE
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	fmt.Println("\n👋 It's being shut down....")
}

// --- CLIENT PROXY YARDIMCILARI ---

func (a *App) startProxy(port int, targetIP string) {
	// Yerel UI (Electron) için dinle
	localAddr := fmt.Sprintf("127.0.0.1:%d", port)
	localListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Printf("❌ Proxy Error (Port %d): %v\n", port, err)
		return
	}
	fmt.Printf("🔗 Proxy Ready: %s -> %s:%d\n", localAddr, targetIP, port)
	for {
		// Electron bağlandı
		localConn, err := localListener.Accept()
		if err != nil {
			continue
		}

		// VPN üzerinden hedefe bağlan
		remoteConn, err := a.Network.Dial(context.Background(), targetIP, port)
		if err != nil {
			fmt.Printf("⚠️ The target could not be connected. (%s:%d): %v\n", targetIP, port, err)
			localConn.Close()
			continue
		}

		// Veriyi taşı
		go pipe(localConn, remoteConn)
		go pipe(remoteConn, localConn)
	}
}

func pipe(src, dst net.Conn) {
	defer src.Close()
	defer dst.Close()
	_, _ = io.Copy(dst, src)
}

// --- DİĞER YARDIMCILAR ---

func mustListen(n *network.Manager, port int) net.Listener {
	ln, err := n.Listen(port)
	if err != nil {
		fmt.Printf("Critical Error: Port %d could not be opened: %v\n", port, err)
		os.Exit(1)
	}
	return ln
}

// --- TRIAL (DENEME SÜRESİ) MANTIĞI ---

type TrialData struct {
	HWID      string    `json:"hwid"`
	UsedMins  int       `json:"used_minutes"`
	LastSeen  time.Time `json:"last_seen"`
}

func getTrialFilePath() string {
	home, _ := os.UserHomeDir()
	// Gizli klasörde tutuyoruz
	dir := filepath.Join(home, ".src-engine")
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "system_info.dat") // İsim yanıltıcı olsun
}

func getHWID() string {
	// Windows WMIC komutu ile Anakart UUID çek
	cmd := exec.Command("wmic", "csproduct", "get", "uuid")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	
	out, err := cmd.Output()
	rawID := ""
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && trimmed != "UUID" {
				rawID = trimmed
				break
			}
		}
	}
	
	if rawID == "" {
		// WMIC çalışmazsa Hostname kullan (Yedek)
		rawID, _ = os.Hostname()
	}

	// Hashle (Okunabilir olmasın)
	hash := sha256.Sum256([]byte(rawID + "SRC-SALT-2025"))
	return hex.EncodeToString(hash[:])
}

func checkTrialLimit() error {
	hwid := getHWID()
	path := getTrialFilePath()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // İlk kez çalışıyor
	}

	var td TrialData
	if err := json.Unmarshal(data, &td); err != nil {
		return nil // Dosya bozuksa sıfırla
	}

	if td.HWID != hwid {
		return nil // Farklı cihaz
	}

	if td.UsedMins >= TrialLimitMinutes {
		return fmt.Errorf("The trial period (%d min) has expired on this device.", TrialLimitMinutes)
	}

	fmt.Printf("⏳ Remaining Time: %d minutes\n", TrialLimitMinutes-td.UsedMins)
	return nil
}

func startTrialTicker() {
	hwid := getHWID()
	path := getTrialFilePath()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		var td TrialData
		data, err := os.ReadFile(path)
		if err == nil {
			_ = json.Unmarshal(data, &td)
		}

		td.HWID = hwid
		td.UsedMins++
		td.LastSeen = time.Now()

		if td.UsedMins > TrialLimitMinutes {
			fmt.Println("\n🛑 TRIAL PERIOD HAS EXPIRED! The application is being shut down....")
			os.Exit(1)
		}

		jsonData, _ := json.Marshal(td)
		_ = os.WriteFile(path, jsonData, 0600)
	}
}