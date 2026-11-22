package sapi

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrEmptyOverwrite: se pidió sobrescribir un archivo con 0 bytes. La nube no lo admite
// (responde MED-1049 «Wrong byte uploaded» y el original queda registrado sin
// contenido y desaparece, comprobado el 20/11/2026): para vaciar un archivo hay que
// subir uno nuevo y retirar el anterior.
var ErrEmptyOverwrite = errors.New("la nube no admite sobrescribir un archivo con 0 bytes (el original se perdería); hay que subir uno nuevo")

// UploadFileToFolderCtx sube un archivo (ver UploadFileToFolder). ctx es el de la
// petición del cliente; la subida web no lo usa todavía.
func (c *Client) UploadFileToFolderCtx(ctx context.Context, fileName string, reader io.Reader, size int64, folderID int64, mimeType string, existingMediaID string, modTime time.Time) (*MediaItem, error) {
	return c.UploadFileToFolder(fileName, reader, size, folderID, mimeType, existingMediaID, modTime)
}

// UploadFileReplacingCtx sobrescribe el archivo existing (mismo ID).
func (c *Client) UploadFileReplacingCtx(ctx context.Context, existing MediaItem, fileName string, reader io.Reader, size int64, folderID int64, mimeType string, modTime time.Time) (*MediaItem, error) {
	return c.UploadFileToFolder(fileName, reader, size, folderID, mimeType, existing.ID, modTime)
}
