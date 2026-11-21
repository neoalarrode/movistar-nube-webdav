package auth

import (
	"fmt"
)

// NewRequestID devuelve un UUID v4 para la cabecera x-request-id, como el cliente oficial.
func NewRequestID() string {
	b := make([]byte, 16)
	mustRandRead(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
