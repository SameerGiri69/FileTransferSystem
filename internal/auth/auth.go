package auth

import (
	"crypto/tls"
	"fmt"

	gomail "gopkg.in/gomail.v2"
)

// SendOTPEmail sends a 6-digit OTP to the given address via Gmail SMTP.
func SendOTPEmail(toEmail, otp, smtpFrom, smtpPass string) error {
	m := gomail.NewMessage()
	m.SetHeader("From", smtpFrom)
	m.SetHeader("To", toEmail)
	m.SetHeader("Subject", "Your FileTransfer verification code")
	m.SetBody("text/html", fmt.Sprintf(`
<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; background:#0a0a0f; color:#e2e8f0; padding:40px;">
  <div style="max-width:480px; margin:auto; background:#13131a; border-radius:16px; padding:40px; border:1px solid #2d2d3d;">
    <h2 style="color:#a78bfa; margin:0 0 8px;">FileTransfer</h2>
    <p style="color:#94a3b8; margin:0 0 32px;">One-Time Verification Code</p>
    <div style="background:#1e1e2e; border-radius:12px; padding:24px; text-align:center; margin-bottom:24px;">
      <span style="font-size:40px; letter-spacing:12px; font-weight:700; color:#a78bfa;">%s</span>
    </div>
    <p style="color:#64748b; font-size:14px;">This code expires in <strong>5 minutes</strong>. Do not share it with anyone.</p>
  </div>
</body>
</html>`, otp))

	d := gomail.NewDialer("smtp.gmail.com", 587, smtpFrom, smtpPass)
	// Allow TLS on port 587 (STARTTLS)
	d.TLSConfig = &tls.Config{InsecureSkipVerify: false, ServerName: "smtp.gmail.com"}

	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("send OTP email: %w", err)
	}
	return nil
}
