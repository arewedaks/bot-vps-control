// Package shell berisi pembantu untuk menjalankan perintah shell dan untuk
// mengutip argumen dengan aman.
//
// Dipisah karena dua hal ini dibutuhkan lebih dari satu fitur: fitur update
// memanggil git dan systemctl, fitur terminal memanggilnya juga, dan fitur
// upload memetiknya saat menyusun perintah. Semuanya helper generik yang
// tidak boleh milik satu fitur tertentu.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"
)

// Run menjalankan cmdStr lewat sh -c dengan batas waktu, lalu mengembalikan
// kode keluar, stdout, dan stderr (keduanya sudah dipangkas spasi tepi).
//
// Timeout killing SELURUH process group, bukan hanya shell-nya: perintah
// seperti "go build" punya anak proses yang bisa tetap hidup setelah
// induknya dibunuh dan memegang kunci berkas.
func Run(cmdStr string, timeoutSec int) (exitCode int, stdout, stderr string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Start(); err != nil {
		return -1, "", fmt.Sprintf("Gagal menjalankan proses: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return -1, "", "⛔ Perintah dibatalkan: Timeout (melebihi batas waktu)."
	case err := <-done:
		code := 0
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				code = exitError.ExitCode()
			} else {
				code = -1
			}
		}
		return code, strings.TrimSpace(out.String()), strings.TrimSpace(errOut.String())
	}
}

// Quote membungkus s dalam kutip tunggal shell, meloloskan kutip di dalamnya.
//
// Dipakai untuk menyisipkan nama berkas atau nilai dari pengguna ke dalam
// perintah shell. Tanpa ini, spasi dan kutip membuat perintah patah atau,
// lebih buruk, menjadi injeksi perintah.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// GetHomeDir mengembalikan direktori home pengguna yang sedang berjalan.
//
// Dipakai untuk menentukan lokasi default yang tidak boleh di-root: data
// deploy, state terminal, berkas sementara. Urutan: os/user lebih_can
// dipercaya daripada env, karena env bisa salah atau dikosongkan di service.
func GetHomeDir() string {
	if usr, err := user.Current(); err == nil && usr.HomeDir != "" {
		return usr.HomeDir
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/root"
}
