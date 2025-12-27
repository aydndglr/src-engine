/*
//go:build windows

package license

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"src-engine-v2/internal/config"
	"syscall"
	"time"
)

// WMIC penceresini gizlemek için
var useSysProcAttr = syscall.SysProcAttr{HideWindow: true}

type Manager struct {
	Config *config.Config
	HWID   string
}

type LicenseRequest struct {
	Key  string `json:"license_key"`
	HWID string `json:"hwid"`
}

type LicenseResponse struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
	Expires string `json:"expires_at"` // ISO8601
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		Config: cfg,
		HWID:   GetHWID(),
	}
}

// Verify: Lisans sunucusuna bağlanıp durumu kontrol eder.
// Eğer sunucuya erişilemezse veya lisans geçersizse error döner.
func (m *Manager) Verify() error {
	// Eğer config'de key yoksa direkt hata
	if m.Config.License.Key == "" {
		return fmt.Errorf("lisans anahtarı boş")
	}

	fmt.Printf("🔐 Lisans Kontrolü Yapılıyor... (ID: %s)\n", m.HWID[:8])

	reqBody := LicenseRequest{
		Key:  m.Config.License.Key,
		HWID: m.HWID,
	}

	jsonBody, _ := json.Marshal(reqBody)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(m.Config.License.ServerURL, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		// Sunucuya ulaşılamadı -> Güvenli modda kapalı kalmalı
		return fmt.Errorf("lisans sunucusuna bağlanılamadı: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("sunucu hatası: %d", resp.StatusCode)
	}

	var result LicenseResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("sunucu cevabı okunamadı")
	}

	if !result.Valid {
		return fmt.Errorf("LİSANS GEÇERSİZ: %s", result.Message)
	}

	fmt.Printf("✅ Lisans Doğrulandı! Bitiş: %s\n", result.Expires)
	return nil
}
	*/

//go:build windows

package license

import (
	"fmt"
	"src-engine-v2/internal/config"
	"syscall"
)

var useSysProcAttr = syscall.SysProcAttr{HideWindow: true}

type Manager struct {
	Config *config.Config
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{Config: cfg}
}

// Verify: Headscale AuthKey var mı kontrol eder.
// Key yoksa FREE/TRIAL moduna izin verir (error dönmez).
func (m *Manager) Verify() error {
	key := ""
	if m.Config != nil {
		key = m.Config.Headscale.AuthKey
	}

	// 1) Anahtar yoksa -> FREE/TRIAL moduna geçilecek (bloklama yok)
	if key == "" {
		fmt.Println("🆓 FREE/TRIAL MODE: Auth Key not entered (trial/free trial mode will be used)")
		return nil
	}

	// 2) Anahtar var ama çok kısaysa -> Geçersiz format (burada blokla)
	// Headscale preauth key örn: "tskey-auth-..." gibi uzun olur.
	if len(key) < 20 { // 10 yerine 20 daha güvenli
		return fmt.Errorf("INVALID_KEY: The key format is incorrect or too short.")
	}

	// 3) Anahtar var -> PRO modu denenecek (asıl doğrulama bağlantıda)
	displayKey := key
	if len(key) > 8 {
		displayKey = key[:8] + "..."
	}
	fmt.Printf("🔐 PRO MODE: License key detected: %s (verification during connection)\n", displayKey)
	return nil
}
