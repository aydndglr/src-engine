package core

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"src-engine-v2/internal/input"
	"src-engine-v2/internal/network"
	"src-engine-v2/internal/protocol"
	"src-engine-v2/internal/video"
)

// Config: Motorun çalışma ayarları
type Config struct {
	Width  int
	Height int
	FPS    int
	RawMode bool
}

// Engine: Sistemin beyni.
type Engine struct {
	NetMgr          *network.Manager
	InputMgr        input.Manager
	Conf            Config
	FrameChan       chan []byte
	ActiveConn      net.Conn
	RequestApproval func(string) bool
}

func NewEngine(mgr *network.Manager, cfg Config) *Engine {
	im, err := input.NewManager()
	if err != nil {
		fmt.Println("⚠️ Input manager hatası:", err)
	}

	return &Engine{
		NetMgr:    mgr,
		InputMgr:  im,
		Conf:      cfg,
		FrameChan: make(chan []byte, 30),
	}
}

func (e *Engine) SetApprovalCallback(cb func(string) bool) {
	e.RequestApproval = cb
}

// --- INTERNAL HELPERS ---

func writeFull(conn net.Conn, b []byte) error {
	for len(b) > 0 {
		n, err := conn.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

func isNetFatal(err error) bool {

	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok {
		if ne.Timeout() || ne.Temporary() {
			return false
		}
	}
	return true
}

// --- HOST MODU (Yayıncı) ---

func (e *Engine) StartHost(port int) error {
	listener, err := e.NetMgr.ListenTCP(port)
	if err != nil {
		return err
	}
	fmt.Printf("🎥 HOST MODU BAŞLADI (TCP Port: %d)\n", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Bağlantı kabul hatası:", err)
			continue
		}

		// 🔥 HOST BOOST: Gelen bağlantının tamponlarını genişlet
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetWriteBuffer(128 * 1024)
			_ = tcpConn.SetReadBuffer(128 * 1024)
			_ = tcpConn.SetNoDelay(true)
		}

		remoteIP, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		fmt.Println("🔒 Bağlantı İsteği Geldi:", remoteIP)

		go e.handleHostConnection(conn)
	}
}

func (e *Engine) handleHostConnection(conn net.Conn) {
	defer conn.Close()
	fmt.Println("✅ Yayın Akışı Başlatıldı!")


	go func() {

		header := make([]byte, 14)
		
		for {
			// Header Oku
			if _, err := io.ReadFull(conn, header); err != nil {
				return
			}

			// Text uzunluğunu al (Son 2 byte)
			textLen := int(binary.LittleEndian.Uint16(header[12:14]))
			
			// Güvenlik kontrolü
			if textLen < 0 || textLen > 256 {
				fmt.Printf("⚠️ Geçersiz Input Text Boyutu: %d\n", textLen)
				return 
			}

			// Payload'ı oluştur (Header + Text)
			payload := make([]byte, 14+textLen)
			copy(payload[:14], header)

			// Varsa Text'i oku
			if textLen > 0 {
				if _, err := io.ReadFull(conn, payload[14:]); err != nil {
					return
				}
			}

			// Decode et ve uygula
			ev, err := protocol.DecodeInputEvent(payload)
			if err == nil && e.InputMgr != nil {
				// Hata vermeden uygula
				// fmt.Printf("🖱️ Input: %v\n", ev) // Debug için açılabilir
				e.InputMgr.Apply(ev)
			} else if err != nil {
				fmt.Println("⚠️ Input Decode Hatası:", err)
			}
		}
	}()

	// 2. VIDEO GÖNDERME HAZIRLIĞI
	capturer := video.NewCapturer(0)
	if err := capturer.Start(); err != nil {
		fmt.Println("Capture start error:", err)
		return
	}
	defer capturer.Close()

	realW, realH := capturer.Size()
	targetW, targetH := realW, realH
	if targetW%2 != 0 {
		targetW--
	}
	if targetH%2 != 0 {
		targetH--
	}

	// FPS'i 25'e sabitliyoruz (Altın Oran)
	e.Conf.FPS = 25

	fmt.Printf("🎥 Yayın Ayarı: %dx%d (Native 1080p) @ %d FPS\n", realW, realH, e.Conf.FPS)

	encoder, err := video.NewEncoder(realW, realH, targetW, targetH, e.Conf.FPS)
	if err != nil {
		fmt.Println("Encoder start error:", err)
		return
	}
	defer encoder.Close()

	// --- 🛡️ SENKRONİZASYON & TRAFİK KONTROLÜ ---
	sendChan := make(chan []byte, 5) // küçük tutuyoruz ki şişmesin
	killSwitch := make(chan bool)

	// capture loop çıkarsa writer da bitsin
	defer close(sendChan)

	// A) GÖNDERİCİ (WRITER) - güvenli writeFull + daha doğru hata davranışı
	go func() {
		defer close(killSwitch)

		sizeBuf := make([]byte, 4)
		consecutiveErrors := 0

		for data := range sendChan {
			// Mobil ağlar için hızlı tepki
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

			binary.LittleEndian.PutUint32(sizeBuf, uint32(len(data)))

			// Header
			if err := writeFull(conn, sizeBuf); err != nil {
				consecutiveErrors++
				fmt.Printf("⚠️ Ağ Hatası (%d/5): %v\n", consecutiveErrors, err)

				// fatal ise anında çık
				if isNetFatal(err) || consecutiveErrors >= 5 {
					return
				}
				continue
			}

			// Data
			if err := writeFull(conn, data); err != nil {
				consecutiveErrors++
				fmt.Printf("⚠️ Ağ Hatası (%d/5): %v\n", consecutiveErrors, err)

				if isNetFatal(err) || consecutiveErrors >= 5 {
					return
				}
				continue
			}

			consecutiveErrors = 0
		}
	}()

	// B) YAKALAYICI (CAPTURER LOOP) - backpressure + adaptive bitrate
	interval := time.Second / time.Duration(e.Conf.FPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Adaptive Bitrate
	// Kademeli ve daha stabil: 800 -> 1200 -> 1800 -> 2500
	levels := []int{800, 1200, 1800, 2500}
	levelIdx := 2 // 1800 başlangıç (2000 yerine yakın ama kademeli)
	currentBitrate := levels[levelIdx]
	encoder.SetBitrate(currentBitrate) // ✅ başlangıç bitrate’i gerçekten uygula

	lastAdjustment := time.Now()
	lastCongested := time.Time{}
	lastRelaxed := time.Time{}

	for {
		select {
		case <-killSwitch:
			fmt.Println("🛑 Yayın durduruldu (Writer Kapandı).")
			return
		case <-ticker.C:
		}

		// ✅ KRİTİK: Kuyruk doluyken boşa encode yapmamalı
		// cap-1'e gelince drop moduna geçiyoruz
		if len(sendChan) >= cap(sendChan)-1 {
			// Sıkışıklık anı
			if lastCongested.IsZero() {
				lastCongested = time.Now()
			}
			// hiçbir şey yapma: capture/encode yok
			continue
		} else {
			// rahat an
			if lastRelaxed.IsZero() {
				lastRelaxed = time.Now()
			}
		}

		// --- 🧠 TRAFİK POLİSİ (ADAPTIVE) ---
		queueSize := len(sendChan)

		// Ayarı çok sık oynatma
		if time.Since(lastAdjustment) > 3*time.Second {
			// Sıkışıklık: queue >= 3
			if queueSize >= 3 {
				// 2 saniyeden uzun sıkışık kaldıysa düşür
				if !lastCongested.IsZero() && time.Since(lastCongested) > 2*time.Second {
					if levelIdx > 0 {
						levelIdx--
						currentBitrate = levels[levelIdx]
						encoder.SetBitrate(currentBitrate)
						fmt.Printf("📉 Ağ tıkandı, kalite düşürülüyor: %d kbps\n", currentBitrate)
					}
					lastAdjustment = time.Now()
					lastCongested = time.Now()
				}
				// rahat sayacını sıfırla
				lastRelaxed = time.Time{}
			} else if queueSize == 0 {
				// Rahatlık: 6 saniye boyunca queue 0 ise yükselt
				if !lastRelaxed.IsZero() && time.Since(lastRelaxed) > 6*time.Second {
					if levelIdx < len(levels)-1 {
						levelIdx++
						currentBitrate = levels[levelIdx]
						encoder.SetBitrate(currentBitrate)
						fmt.Printf("📈 Ağ rahatladı, kalite artırılıyor: %d kbps\n", currentBitrate)
					}
					lastAdjustment = time.Now()
					lastRelaxed = time.Now()
				}
				// sıkışık sayacını sıfırla
				lastCongested = time.Time{}
			} else {
				// orta durum: sayacı resetleme, sadece aşırı oynamayı engelle
				lastCongested = time.Time{}
				lastRelaxed = time.Time{}
			}
		}

		img, err := capturer.Capture()
		if err != nil {
			continue
		}

		h264Data := encoder.Encode(img)
		if len(h264Data) == 0 {
			continue
		}

		select {
		case sendChan <- h264Data:
			// ok
		case <-killSwitch:
			return
		default:
			// 🗑️ DROP FRAME: dolduysa at (latency artmasın, donma olmasın)
		}
	}
}

// --- CLIENT MODU (İzleyici) ---

func (e *Engine) StartClient(targetIP string, port int) error {
	conn, err := e.NetMgr.DialTCP(targetIP, port)
	if err != nil {
		return err
	}

	e.ActiveConn = conn
	fmt.Println("📺 SPECTATOR MODE: Connection established. ->", targetIP)

	defer conn.Close()

	sizeBuf := make([]byte, 4)

	// ✅ Buffer reuse: her framede make() yapıp GC şişirmeyelim
	var frameBuf []byte

	for {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

		if _, err := io.ReadFull(conn, sizeBuf); err != nil {
			fmt.Println("⚠️ Data flow interrupted.:", err)
			close(e.FrameChan)
			return err
		}

		frameSize := binary.LittleEndian.Uint32(sizeBuf)
		if frameSize == 0 || frameSize > 10*1024*1024 {
			close(e.FrameChan)
			return fmt.Errorf("invalid frame size")
		}

		need := int(frameSize)
		if cap(frameBuf) < need {
			frameBuf = make([]byte, need)
		}
		frameData := frameBuf[:need]

		if _, err := io.ReadFull(conn, frameData); err != nil {
			close(e.FrameChan)
			return err
		}

		// FrameChan consumer tarafı yavaşsa drop et (donma yerine akıcılık)
		out := make([]byte, len(frameData))
		copy(out, frameData)

		select {
		case e.FrameChan <- out:
		default:
			// drop
		}
	}
}

func (e *Engine) SendInput(ev protocol.InputEvent) error {
	if e.ActiveConn == nil {
		return fmt.Errorf("No connection")
	}
	data, err := protocol.EncodeInputEvent(ev)
	if err != nil {
		return err
	}
	_ = e.ActiveConn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = e.ActiveConn.Write(data)
	return err
}