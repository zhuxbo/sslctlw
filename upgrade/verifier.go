package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	wintrust           = syscall.NewLazyDLL("wintrust.dll")
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	procWinVerifyTrust                 = wintrust.NewProc("WinVerifyTrust")
	procWTHelperProvDataFromStateData  = wintrust.NewProc("WTHelperProvDataFromStateData")
	procWTHelperGetProvSignerFromChain = wintrust.NewProc("WTHelperGetProvSignerFromChain")
	procWTHelperGetProvCertFromChain   = wintrust.NewProc("WTHelperGetProvCertFromChain")
	procCertGetNameStringW             = crypt32.NewProc("CertGetNameStringW")
)

// Windows 常量
const (
	INVALID_HANDLE_VALUE        = ^uintptr(0)
	WTD_UI_NONE                 = 2
	WTD_REVOKE_NONE             = 0
	WTD_REVOKE_WHOLECHAIN       = 1
	WTD_CHOICE_FILE             = 1
	WTD_STATEACTION_VERIFY      = 1
	WTD_STATEACTION_CLOSE       = 2
	TRUST_E_NOSIGNATURE         = 0x800B0100
	TRUST_E_SUBJECT_NOT_TRUSTED = 0x800B0004
	TRUST_E_PROVIDER_UNKNOWN    = 0x800B0001
	CERT_NAME_SIMPLE_DISPLAY_TYPE = 4
	CERT_NAME_ATTR_TYPE           = 3
	szOID_ORGANIZATION_NAME       = "2.5.4.10"
	szOID_COUNTRY_NAME            = "2.5.4.6"
)

// GUID for WINTRUST_ACTION_GENERIC_VERIFY_V2
var WINTRUST_ACTION_GENERIC_VERIFY_V2 = syscall.GUID{
	Data1: 0xaac56b,
	Data2: 0xcd44,
	Data3: 0x11d0,
	Data4: [8]byte{0x8c, 0xc2, 0x00, 0xc0, 0x4f, 0xc2, 0x95, 0xee},
}

// WINTRUST_FILE_INFO 结构
type WINTRUST_FILE_INFO struct {
	cbStruct       uint32
	pcwszFilePath  *uint16
	hFile          syscall.Handle
	pgKnownSubject *syscall.GUID
}

// WINTRUST_DATA 结构
type WINTRUST_DATA struct {
	cbStruct            uint32
	pPolicyCallbackData uintptr
	pSIPClientData      uintptr
	dwUIChoice          uint32
	fdwRevocationChecks uint32
	dwUnionChoice       uint32
	pFile               *WINTRUST_FILE_INFO
	dwStateAction       uint32
	hWVTStateData       syscall.Handle
	pwszURLReference    *uint16
	dwProvFlags         uint32
	dwUIContext         uint32
	pSignatureSettings  uintptr
}

// CERT_CONTEXT Windows 证书上下文
type CERT_CONTEXT struct {
	dwCertEncodingType uint32
	pbCertEncoded      *byte
	cbCertEncoded      uint32
	pCertInfo          uintptr
	hCertStore         syscall.Handle
}

// CRYPT_PROVIDER_CERT WinVerifyTrust 提供者证书结构（仅定义需要的字段）
type CRYPT_PROVIDER_CERT struct {
	cbStruct uint32
	pCert    *CERT_CONTEXT
}

// AuthenticodeVerifier Authenticode 签名验证器
type AuthenticodeVerifier struct{}

// NewAuthenticodeVerifier 创建验证器
func NewAuthenticodeVerifier() *AuthenticodeVerifier {
	return &AuthenticodeVerifier{}
}

// Verify 验证文件签名
func (v *AuthenticodeVerifier) Verify(filePath string, config *VerifyConfig) (*VerifyResult, error) {
	result := &VerifyResult{
		Valid: false,
	}

	// 验证签名并提取证书信息（合并执行，避免分离导致的证书查找失败）
	certInfo, err := v.verifyAndExtractCert(filePath)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	result.Fingerprint = certInfo.fingerprint
	result.Subject = certInfo.subject
	result.Organization = certInfo.organization
	result.Country = certInfo.country
	result.Issuer = certInfo.issuer
	result.Valid = true

	if config == nil {
		result.Message = "签名验证通过"
		return result, nil
	}

	// 验证组织名称（必须配置，防止未注入 buildTrustedOrg 时绕过验证）
	if config.TrustedOrg == "" {
		result.Valid = false
		result.Message = "安全配置错误: 未配置可信组织名称（buildTrustedOrg 未注入）"
		return result, nil
	}
	if !strings.EqualFold(certInfo.organization, config.TrustedOrg) {
		result.Valid = false
		result.Message = fmt.Sprintf("组织名称不匹配: 期望 %s, 实际 %s", config.TrustedOrg, certInfo.organization)
		return result, nil
	}

	// 验证国家代码
	if config.TrustedCountry != "" {
		if !strings.EqualFold(certInfo.country, config.TrustedCountry) {
			result.Valid = false
			result.Message = fmt.Sprintf("国家代码不匹配: 期望 %s, 实际 %s", config.TrustedCountry, certInfo.country)
			return result, nil
		}
	}

	// 验证 CA
	if len(config.TrustedCAs) > 0 {
		caMatched := false
		for _, ca := range config.TrustedCAs {
			if strings.Contains(strings.ToLower(certInfo.issuer), strings.ToLower(ca)) {
				caMatched = true
				break
			}
		}
		if !caMatched {
			result.Valid = false
			result.Message = fmt.Sprintf("CA 不在可信列表中: %s", certInfo.issuer)
			return result, nil
		}
	}

	// 全部验证通过
	result.Message = "签名验证通过"
	return result, nil
}

// verifyAndExtractCert 验证签名并从 WinVerifyTrust 状态数据中提取证书信息
// 合并 verifySignature + getCertificateInfo，解决证书未嵌入 PKCS7 导致的查找失败
func (v *AuthenticodeVerifier) verifyAndExtractCert(filePath string) (*certificateInfo, error) {
	filePathPtr, err := syscall.UTF16PtrFromString(filePath)
	if err != nil {
		return nil, fmt.Errorf("签名验证失败: %v", err)
	}

	fileInfo := WINTRUST_FILE_INFO{
		cbStruct:      uint32(unsafe.Sizeof(WINTRUST_FILE_INFO{})),
		pcwszFilePath: filePathPtr,
	}

	trustData := WINTRUST_DATA{
		cbStruct:            uint32(unsafe.Sizeof(WINTRUST_DATA{})),
		dwUIChoice:          WTD_UI_NONE,
		fdwRevocationChecks: WTD_REVOKE_WHOLECHAIN,
		dwUnionChoice:       WTD_CHOICE_FILE,
		pFile:               &fileInfo,
		dwStateAction:       WTD_STATEACTION_VERIFY,
	}

	closeTrust := func() {
		trustData.dwStateAction = WTD_STATEACTION_CLOSE
		procWinVerifyTrust.Call(
			INVALID_HANDLE_VALUE,
			uintptr(unsafe.Pointer(&WINTRUST_ACTION_GENERIC_VERIFY_V2)),
			uintptr(unsafe.Pointer(&trustData)),
		)
	}

	// 先尝试带吊销检查的验证
	ret, _, _ := procWinVerifyTrust.Call(
		INVALID_HANDLE_VALUE,
		uintptr(unsafe.Pointer(&WINTRUST_ACTION_GENERIC_VERIFY_V2)),
		uintptr(unsafe.Pointer(&trustData)),
	)

	if ret != 0 {
		// 吊销检查失败，降级为不检查吊销
		closeTrust()

		trustData.fdwRevocationChecks = WTD_REVOKE_NONE
		trustData.dwStateAction = WTD_STATEACTION_VERIFY

		ret, _, _ = procWinVerifyTrust.Call(
			INVALID_HANDLE_VALUE,
			uintptr(unsafe.Pointer(&WINTRUST_ACTION_GENERIC_VERIFY_V2)),
			uintptr(unsafe.Pointer(&trustData)),
		)

		if ret != 0 {
			closeTrust()
			switch ret {
			case TRUST_E_NOSIGNATURE:
				return nil, fmt.Errorf("签名验证失败: 文件未签名")
			case TRUST_E_SUBJECT_NOT_TRUSTED:
				return nil, fmt.Errorf("签名验证失败: 签名者不受信任")
			case TRUST_E_PROVIDER_UNKNOWN:
				return nil, fmt.Errorf("签名验证失败: 未知的信任提供程序")
			default:
				return nil, fmt.Errorf("签名验证失败: 0x%X", ret)
			}
		}
	}

	// 签名验证通过，从状态数据中提取证书信息
	certInfo, extractErr := v.extractCertFromState(trustData.hWVTStateData)
	closeTrust()
	runtime.KeepAlive(filePathPtr)

	if extractErr != nil {
		return nil, fmt.Errorf("获取证书信息失败: %v", extractErr)
	}

	return certInfo, nil
}

// extractCertFromState 从 WinVerifyTrust 状态数据中提取签名者证书信息
func (v *AuthenticodeVerifier) extractCertFromState(hStateData syscall.Handle) (*certificateInfo, error) {
	pProvData, _, _ := procWTHelperProvDataFromStateData.Call(uintptr(hStateData))
	if pProvData == 0 {
		return nil, fmt.Errorf("WTHelperProvDataFromStateData 失败")
	}

	pProvSigner, _, _ := procWTHelperGetProvSignerFromChain.Call(pProvData, 0, 0, 0)
	if pProvSigner == 0 {
		return nil, fmt.Errorf("WTHelperGetProvSignerFromChain 失败")
	}

	pProvCert, _, _ := procWTHelperGetProvCertFromChain.Call(pProvSigner, 0)
	if pProvCert == 0 {
		return nil, fmt.Errorf("WTHelperGetProvCertFromChain 失败")
	}

	provCert := *(**CRYPT_PROVIDER_CERT)(unsafe.Pointer(&pProvCert))
	if provCert.pCert == nil {
		return nil, fmt.Errorf("证书上下文为空")
	}

	info := &certificateInfo{}

	// 计算指纹 (SHA-256)
	certData := unsafe.Slice(provCert.pCert.pbCertEncoded, provCert.pCert.cbCertEncoded)
	hash := sha256.Sum256(certData)
	info.fingerprint = strings.ToUpper(hex.EncodeToString(hash[:]))

	// 获取证书名称信息
	certCtxPtr := uintptr(unsafe.Pointer(provCert.pCert))
	info.subject = v.getCertNameString(certCtxPtr, CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, "")
	info.organization = v.getCertNameString(certCtxPtr, CERT_NAME_ATTR_TYPE, 0, szOID_ORGANIZATION_NAME)
	info.country = v.getCertNameString(certCtxPtr, CERT_NAME_ATTR_TYPE, 0, szOID_COUNTRY_NAME)
	info.issuer = v.getCertNameString(certCtxPtr, CERT_NAME_SIMPLE_DISPLAY_TYPE, 0x1, "")

	return info, nil
}

// certificateInfo 证书信息
type certificateInfo struct {
	fingerprint  string
	subject      string
	organization string
	country      string
	issuer       string
}

// getCertNameString 获取证书名称字符串
// dwFlags: 0 获取主题名称，CERT_NAME_ISSUER_FLAG (0x1) 获取颁发者名称
func (v *AuthenticodeVerifier) getCertNameString(certCtx uintptr, dwType uint32, dwFlags uint32, pszOID string) string {
	var oidPtr uintptr
	var oidBytes []byte
	if pszOID != "" {
		oidBytes = append([]byte(pszOID), 0)
		oidPtr = uintptr(unsafe.Pointer(&oidBytes[0]))
	}

	// 获取所需缓冲区大小
	sizeRet, _, _ := procCertGetNameStringW.Call(
		certCtx,
		uintptr(dwType),
		uintptr(dwFlags),
		oidPtr,
		0,
		0,
	)

	// 范围检查并转换为 int
	if sizeRet <= 1 || sizeRet > 4096 {
		runtime.KeepAlive(oidBytes)
		return ""
	}
	size := int(sizeRet)

	// 获取名称
	buf := make([]uint16, size)
	procCertGetNameStringW.Call(
		certCtx,
		uintptr(dwType),
		uintptr(dwFlags),
		oidPtr,
		uintptr(unsafe.Pointer(&buf[0])),
		sizeRet,
	)

	// 确保 oidBytes 在 syscall 期间不被 GC
	runtime.KeepAlive(oidBytes)

	return syscall.UTF16ToString(buf)
}
