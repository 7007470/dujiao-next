// Package credential 加密保存代练模块的游戏账号凭据、会话和用户输入的验证码。
// AES-256-GCM（复用 internal/crypto），密钥独立于 app.secret_key，按版本号保存；
// 密文格式 "v{版本}:{hex}"。轮换时新增一个版本并把 current 指向它，旧密文仍用原版本解密。
package credential

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dujiao-next/internal/crypto"
)

// MinKeyLength 密钥字符串最短长度（建议 openssl rand -hex 32）。
const MinKeyLength = 32

// Sealer 实现 boostcontract.CredentialSealer。
type Sealer struct {
	current int
	keys    map[int][]byte
}

// New 用 版本 → 密钥字符串 创建加密器。current 必须在 keys 里；每个密钥至少 MinKeyLength 个字符，且互不相同。
func New(keys map[int]string, current int) (*Sealer, error) {
	if len(keys) == 0 {
		return nil, errors.New("boost credential: no keys configured")
	}
	if _, ok := keys[current]; !ok {
		return nil, fmt.Errorf("boost credential: current key version %d not configured", current)
	}
	seen := map[string]bool{}
	out := &Sealer{current: current, keys: map[int][]byte{}}
	for version, raw := range keys {
		key := strings.TrimSpace(raw)
		if version <= 0 {
			return nil, fmt.Errorf("boost credential: key version must be positive, got %d", version)
		}
		if len(key) < MinKeyLength {
			return nil, fmt.Errorf("boost credential: key version %d is shorter than %d characters", version, MinKeyLength)
		}
		if seen[key] {
			return nil, errors.New("boost credential: key versions must use different keys")
		}
		seen[key] = true
		out.keys[version] = crypto.DeriveKey("boost-credential:" + key)
	}
	return out, nil
}

// FromConfig 解析配置里的 {"1": "密钥", "2": "密钥"}。keys 为空时返回 (nil, nil)，表示未启用账号绑定。
func FromConfig(keys map[string]string, current int) (*Sealer, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	parsed := make(map[int]string, len(keys))
	for raw, key := range keys {
		version, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
		if err != nil {
			return nil, fmt.Errorf("boost credential: key version %q is not a number", raw)
		}
		parsed[version] = key
	}
	if current == 0 && len(parsed) == 1 {
		for v := range parsed {
			current = v
		}
	}
	return New(parsed, current)
}

// KeyVersion 当前加密使用的密钥版本。
func (s *Sealer) KeyVersion() int { return s.current }

// Seal 用当前密钥加密。
func (s *Sealer) Seal(plaintext string) (string, int, error) {
	cipher, err := crypto.Encrypt(s.keys[s.current], plaintext)
	if err != nil {
		return "", 0, err
	}
	return "v" + strconv.Itoa(s.current) + ":" + cipher, s.current, nil
}

// Open 按密文前缀里的版本解密。错误信息不包含密文或明文。
func (s *Sealer) Open(sealed string) (string, error) {
	prefix, body, ok := strings.Cut(sealed, ":")
	if !ok || !strings.HasPrefix(prefix, "v") {
		return "", errors.New("boost credential: malformed cipher")
	}
	version, err := strconv.Atoi(prefix[1:])
	if err != nil {
		return "", errors.New("boost credential: malformed cipher")
	}
	key, ok := s.keys[version]
	if !ok {
		return "", fmt.Errorf("boost credential: key version %d not configured", version)
	}
	plain, err := crypto.Decrypt(key, body)
	if err != nil {
		return "", errors.New("boost credential: decrypt failed")
	}
	return plain, nil
}
