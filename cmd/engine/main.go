package main

import (
    "context"
    "encoding/binary"
    "flag"
    "fmt"
    "io"
    "log"
    "net"
    "os"
    "os/signal"
    "path/filepath"
    "runtime"
    "runtime/debug"
    "sync"
    "sync/atomic"
    "syscall"
    "time"

    "src-engine/internal/audio"
    "src-engine/internal/clipboard"
    "src-engine/internal/core"
    "src-engine/internal/network"
    "src-engine/internal/protocol"
)

// UI Durum Yönetimi
var (
    uiConnected bool
    uiConnMutex sync.Mutex
)

// --- NABIZ VE LOGLAMA FONKSİYONU ---
func startDebugLogger() {
    f, err := os.OpenFile("debug_log.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
    if err != nil {
        fmt.Println("⚠️ Log dosyası oluşturulamadı:", err)
        return
    }

    multiWriter := io.MultiWriter(os.Stdout, f)
    log.SetOutput(multiWriter)

    go func() {
        for {
            var m runtime.MemStats
            runtime.ReadMemStats(&m)
            // Log kirliliği yapmasın diye süreyi uzattım
            time.Sleep(10 * time.Second)
        }
    }()
}

func main() {
    startDebugLogger()
    log.Println("🚀 MOTOR BAŞLATILIYOR... (Debug Modu)")

    defer func() {
        if r := recover(); r != nil {
            log.Printf("🔥 KRİTİK HATA (PANIC): %v\n", r)
            log.Println(string(debug.Stack()))
            time.Sleep(2 * time.Second)
        }
    }()

    hostname, err := os.Hostname()
    if err != nil {
        hostname = "unknown-device"
    }
    log.Printf("💻 Cihaz Kimliği: %s\n", hostname)

    controlURL := flag.String("url", "https://vpn.cybervpn.tr", "Headscale URL")
    authKey := flag.String("key", "", "Auth Key")
    connectIP := flag.String("connect", "", "Hedef IP (Sadece Client Modu için)")
    uiPort := flag.Int("ui-port", 9000, "UI (Electron) Portu")
    width := flag.Int("w", 0, "Genişlik (0 = Otomatik)")
    height := flag.Int("h", 0, "Yükseklik (0 = Otomatik)")
    fps := flag.Int("fps", 30, "FPS")
    rawMode := flag.Bool("raw", false, "Ham video modu")

    flag.Parse()

    if *authKey == "" {
        log.Fatal("❌ HATA: -key parametresi zorunlu!")
    }

    // --- NETWORK ---
    netMgr, err := network.NewManager(hostname, *authKey, *controlURL)
    if err != nil {
        log.Fatalf("Network hatası: %v", err)
    }

    if err := netMgr.StartTunnel(); err != nil {
        log.Fatalf("Tünel hatası: %v", err)
    }

    log.Printf("STATUS:READY,IP:%s,HOST:%s\n", netMgr.MyIP, hostname)

    // --- CLIPBOARD ---
    if err := clipboard.Init(); err != nil {
        log.Println("⚠️ Pano sistemi başlatılamadı:", err)
    }
    clipMgr := clipboard.NewManager()
    clipMgr.StartWatcher(context.Background())

    // --- AUDIO ---
    var audioMgr *audio.Manager = nil

    // --- ENGINE ---
    engineCfg := core.Config{Width: *width, Height: *height, FPS: *fps, RawMode: *rawMode}
    eng := core.NewEngine(netMgr, engineCfg)

    // --- SIGNALS ---
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

    if *connectIP == "" {
        // --- HOST MODE ---
        go func() {
            if err := eng.StartHost(44444); err != nil {
                log.Fatalf("Host hatası: %v", err)
            }
        }()

        go func() {
            l, err := netMgr.ListenTCP(44445)
            if err != nil {
                log.Printf("Veri Kanalı Hatası: %v", err)
                return
            }
            for {
                conn, err := l.Accept()
                if err != nil {
                    continue
                }
                go handleDataSession(conn, clipMgr, audioMgr)
            }
        }()

        go startUIServer(*uiPort, eng)
        log.Println("🎥 Mod: SUNUCU (Bağlantı bekleniyor...)")
        <-sigs
    } else {
        // --- CLIENT MODE ---
        log.Printf("📺 Mod: İZLEYİCİ (Hedef: %s)\n", *connectIP)

        go func() {
            conn, err := netMgr.DialTCP(*connectIP, 44445)
            if err != nil {
                return
            }
            handleDataSession(conn, clipMgr, audioMgr)
        }()

        go startUIServer(*uiPort, eng)

        go func() {
            if err := eng.StartClient(*connectIP, 44444); err != nil {
                os.Exit(1)
            }
        }()
        <-sigs
    }

    log.Println("👋 Kapatılıyor...")
}

// --- UI SERVER ---
func startUIServer(port int, eng *core.Engine) {
    l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("🔌 UI Köprüsü Hazır: 127.0.0.1:%d\n", port)

    for {
        conn, err := l.Accept()
        if err != nil {
            continue
        }

        if tcpConn, ok := conn.(*net.TCPConn); ok {
            _ = tcpConn.SetWriteBuffer(512 * 1024)
            _ = tcpConn.SetReadBuffer(512 * 1024)
            _ = tcpConn.SetNoDelay(true)
        }

        uiConnMutex.Lock()
        uiConnected = true
        uiConnMutex.Unlock()

        go handleUIDataTransfer(conn, eng)
    }
}

// --- DATA CHANNEL ---
func handleDataSession(conn net.Conn, cm *clipboard.ClipboardManager, am *audio.Manager) {
    defer conn.Close()
    var currentFile *os.File
    var currentFileSize int64
    var receivedBytes int64

    alive := atomic.Bool{}
    alive.Store(true)
    defer alive.Store(false)

    // Pano dinleyicisi
    cm.SetCallback(func(text string) {
        if alive.Load() {
             _ = protocol.WriteDataPacket(conn, protocol.DataTypeClipboard, []byte(text))
        }
    })
    defer cm.SetCallback(nil)

    for {
        header, err := protocol.ReadDataHeader(conn)
        if err != nil {
            return
        }
        
        // Güvenlik limiti (128MB)
        if header.Size > 128*1024*1024 {
            return
        }

        data := make([]byte, header.Size)
        if _, err := io.ReadFull(conn, data); err != nil {
            return
        }

        switch header.Type {
        case protocol.DataTypeClipboard:
            cm.Write(string(data))
        case protocol.DataTypeFileStart:
            meta, _ := protocol.DecodeFileStart(data)
            home, _ := os.UserHomeDir()
            downloadDir := filepath.Join(home, "Downloads")
            _ = os.MkdirAll(downloadDir, 0755)
            fullPath := filepath.Join(downloadDir, filepath.Base(meta.Name))
            f, _ := os.Create(fullPath)
            currentFile = f
            currentFileSize = meta.Size
            receivedBytes = 0
        case protocol.DataTypeFileData:
            if currentFile != nil {
                n, _ := currentFile.Write(data)
                receivedBytes += int64(n)
                if receivedBytes >= currentFileSize {
                    _ = currentFile.Close()
                    currentFile = nil
                }
            }
        }
    }
}

// --- UI BRIDGE (Video + Input Fix) ---
const uiWriteTimeout = 200 * time.Millisecond

func handleUIDataTransfer(uiConn net.Conn, eng *core.Engine) {
    defer func() {
        _ = uiConn.Close()
        uiConnMutex.Lock()
        uiConnected = false
        uiConnMutex.Unlock()
    }()

    // A) Motor -> UI (Video Akışı)
    go func() {
        defer func() { _ = recover() }()
        header := make([]byte, 4)
        for {
            frame, ok := <-eng.FrameChan
            if !ok {
                return
            }
            // Electron'a göndermeden önce header ekle
            if !eng.Conf.RawMode {
                binary.LittleEndian.PutUint32(header, uint32(len(frame)))
                if _, err := uiConn.Write(header); err != nil {
                     return
                }
            }
            if _, err := uiConn.Write(frame); err != nil {
                return
            }
        }
    }()

    // B) UI -> Motor (Input) - 🔥 DÜZELTİLDİ
    // Protocol/types.go içerisinde inputHeaderV2Size = 14 olarak tanımlı.
    // Frontend V2 header gönderiyor (14 byte).
    headerBuf := make([]byte, 14) 

    for {
        // 1. Header'ı tam olarak oku (14 Byte)
        _, err := io.ReadFull(uiConn, headerBuf)
        if err != nil {
            return // Bağlantı koptu
        }

        // 2. Text uzunluğunu kontrol et (Son 2 byte)
        textLen := binary.LittleEndian.Uint16(headerBuf[12:14])
        
        var text string
        if textLen > 0 && textLen <= 256 { // Güvenlik limiti
            textBytes := make([]byte, textLen)
            _, err := io.ReadFull(uiConn, textBytes)
            if err != nil {
                return
            }
            text = string(textBytes)
        }

        // 3. Event'i oluştur
        ev := protocol.InputEvent{
            Device: protocol.InputDevice(headerBuf[0]),
            Action: protocol.InputAction(headerBuf[1]),
            Flags:  headerBuf[2],
            // Byte 3 padding
            X:      binary.LittleEndian.Uint16(headerBuf[4:6]),
            Y:      binary.LittleEndian.Uint16(headerBuf[6:8]),
            Wheel:  int16(binary.LittleEndian.Uint16(headerBuf[8:10])),
            Key:    binary.LittleEndian.Uint16(headerBuf[10:12]),
            Text:   text,
        }

        // 4. Engine'e gönder (O da WindowsManager'a iletecek)
        eng.SendInput(ev)
    }
}