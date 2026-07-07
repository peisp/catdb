package postgresdrv

import "encoding/base64"

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
