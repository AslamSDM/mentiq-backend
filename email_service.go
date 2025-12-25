package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/resend/resend-go/v3"
)

// EmailService handles sending emails via Resend
type EmailService struct {
	apiKey    string
	fromEmail string
	fromName  string
	baseURL   string
	client    *resend.Client
}

// NewEmailService creates a new email service instance
func NewEmailService() *EmailService {
	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" {
		log.Println("Warning: RESEND_API_KEY not set. Email features will be disabled.")
	}

	var client *resend.Client
	if apiKey != "" {
		client = resend.NewClient(apiKey)
	}

	return &EmailService{
		apiKey:    apiKey,
		fromEmail: os.Getenv("EMAIL_FROM_ADDRESS"),
		fromName:  os.Getenv("EMAIL_FROM_NAME"),
		baseURL:   os.Getenv("FRONTEND_URL"),
		client:    client,
	}
}

// sendEmail is the internal helper to send emails via Resend
func (es *EmailService) sendEmail(toEmail, toName, subject, htmlContent, plainTextContent string) error {
	if es.apiKey == "" || es.client == nil {
		log.Printf("Resend not configured, skipping email to %s", toEmail)
		return nil
	}

	fromAddress := fmt.Sprintf("%s <%s>", es.fromName, es.fromEmail)

	params := &resend.SendEmailRequest{
		From:    fromAddress,
		To:      []string{toEmail},
		Subject: subject,
		Html:    htmlContent,
		Text:    plainTextContent,
	}

	sent, err := es.client.Emails.Send(params)
	if err != nil {
		log.Printf("Error sending email to %s via Resend: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %w", err)
	}

	log.Printf("Email sent successfully to %s. Message ID: %s", toEmail, sent.Id)
	return nil
}

// SendInvitationEmail sends a team invitation email
func (es *EmailService) SendInvitationEmail(toEmail, toName, inviterName, accountName, token string) error {
	log.Printf("Preparing to send invitation email to %s (Name: %s) for account %s initiated by %s", toEmail, toName, accountName, inviterName)

	if es.apiKey == "" {
		log.Println("Resend not configured, skipping email send")
		log.Printf("Would have sent invitation to %s from %s for account %s with token %s", toEmail, inviterName, accountName, token)
		return nil
	}

	acceptURL := fmt.Sprintf("%s/accept-invitation?token=%s", es.baseURL, token)

	subject := fmt.Sprintf("%s invited you to join %s on Mentiq", inviterName, accountName)

	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin: 0; padding: 0; font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; background-color: #000000; color: #ffffff;">
    <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0" style="background-color: #000000;">
        <tr>
            <td align="center" style="padding: 40px 0;">
                <table role="presentation" width="600" cellspacing="0" cellpadding="0" border="0" style="background-color: #111111; border-radius: 12px; border: 1px solid #333333; box-shadow: 0 4px 20px rgba(0,0,0,0.5);">
                    <!-- Header -->
                    <tr>
                        <td style="padding: 40px 40px 30px; text-align: center; border-bottom: 1px solid #222222;">
                            <h1 style="margin: 0; color: #ffffff; font-size: 28px; font-weight: 700; letter-spacing: -0.5px;">Mentiq</h1>
                        </td>
                    </tr>

                    <!-- Body -->
                    <tr>
                        <td style="padding: 40px;">
                            <h2 style="margin: 0 0 20px; color: #ffffff; font-size: 24px; font-weight: 600;">You're Invited!</h2>
                            
                            <p style="margin: 0 0 20px; color: #cccccc; font-size: 16px; line-height: 1.6;">
                                Hi%s,
                            </p>

                            <p style="margin: 0 0 20px; color: #cccccc; font-size: 16px; line-height: 1.6;">
                                <strong>%s</strong> has invited you to join their team on <strong>Mentiq</strong>, the platform for analyzing, optimizing, and retaining your users.
                            </p>

                            <p style="margin: 0 0 30px; color: #999999; font-size: 14px; line-height: 1.6;">
                                Click the button below to accept the invitation and set up your account.
                            </p>

                            <!-- CTA Button -->
                            <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0">
                                <tr>
                                    <td align="center" style="padding: 10px 0 30px;">
                                        <a href="%s" style="display: inline-block; background: linear-gradient(135deg, #7c3aed 0%%, #4f46e5 100%%); color: #ffffff; text-decoration: none; padding: 14px 40px; border-radius: 8px; font-size: 16px; font-weight: 600; box-shadow: 0 4px 12px rgba(124, 58, 237, 0.3);">
                                            Accept Invitation
                                        </a>
                                    </td>
                                </tr>
                            </table>

                            <!-- Alternative Link -->
                            <p style="margin: 20px 0 0; padding: 15px; background-color: #1a1a1a; border-radius: 6px; color: #888888; font-size: 12px; line-height: 1.6; border: 1px solid #333333;">
                                <strong>Or copy and paste this link:</strong><br>
                                <a href="%s" style="color: #a78bfa; word-break: break-all; text-decoration: none;">%s</a>
                            </p>

                            <!-- Expiration Notice -->
                            <p style="margin: 30px 0 0; color: #666666; font-size: 13px; line-height: 1.6; text-align: center; border-top: 1px solid #222222; padding-top: 20px;">
                                This invitation expires in 7 days.
                            </p>
                        </td>
                    </tr>

                    <!-- Footer -->
                    <tr>
                        <td style="padding: 30px; text-align: center; background-color: #0a0a0a; border-radius: 0 0 12px 12px; border-top: 1px solid #222222;">
                            <p style="margin: 0; color: #555555; font-size: 12px;">
                                © 2025 Mentiq. All rights reserved.<br>
                                If you didn't expect this invitation, you can safely ignore this email.
                            </p>
                        </td>
                    </tr>
                </table>
            </td>
        </tr>
    </table>
</body>
</html>
`, getName(toName), inviterName, acceptURL, acceptURL, acceptURL)

	plainTextContent := fmt.Sprintf(`
You've been invited to join %s on Mentiq!

Hi%s,

%s has invited you to join their team on Mentiq.

Click the link below to accept the invitation and set up your account:
%s

This invitation expires in 7 days.

If you didn't expect this invitation, you can safely ignore this email.
`, accountName, getName(toName), inviterName, acceptURL)

	return es.sendEmail(toEmail, toName, subject, htmlContent, plainTextContent)
}

// getName formats the name for email greeting
func getName(name string) string {
	if name != "" {
		return " " + name
	}
	return ""
}

// SendVerificationEmail sends an email verification email
func (es *EmailService) SendVerificationEmail(toEmail, toName, token string) error {
	log.Printf("Preparing to send verification email to %s", toEmail)

	if es.apiKey == "" {
		log.Println("Resend not configured, skipping email send")
		log.Printf("Would have sent verification email to %s with token %s", toEmail, token)
		return nil
	}

	verifyURL := fmt.Sprintf("%s/verify-email?token=%s", es.baseURL, token)

	subject := "Verify your email address - Mentiq"

	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin: 0; padding: 0; font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; background-color: #000000; color: #ffffff;">
    <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0" style="background-color: #000000;">
        <tr>
            <td align="center" style="padding: 40px 0;">
                <table role="presentation" width="600" cellspacing="0" cellpadding="0" border="0" style="background-color: #111111; border-radius: 12px; border: 1px solid #333333; box-shadow: 0 4px 20px rgba(0,0,0,0.5);">
                    <!-- Header -->
                    <tr>
                        <td style="padding: 40px 40px 30px; text-align: center; border-bottom: 1px solid #222222;">
                            <h1 style="margin: 0; color: #ffffff; font-size: 28px; font-weight: 700; letter-spacing: -0.5px;">Mentiq</h1>
                        </td>
                    </tr>

                    <!-- Body -->
                    <tr>
                        <td style="padding: 40px;">
                            <h2 style="margin: 0 0 20px; color: #ffffff; font-size: 24px; font-weight: 600;">Verify Your Email</h2>
                            
                            <p style="margin: 0 0 20px; color: #cccccc; font-size: 16px; line-height: 1.6;">
                                Hi%s,
                            </p>

                            <p style="margin: 0 0 20px; color: #cccccc; font-size: 16px; line-height: 1.6;">
                                Thanks for signing up for <strong>Mentiq</strong>! Please verify your email address to activate your account and start analyzing, optimizing, and retaining your users.
                            </p>

                            <p style="margin: 0 0 30px; color: #999999; font-size: 14px; line-height: 1.6;">
                                Click the button below to verify your email address.
                            </p>

                            <!-- CTA Button -->
                            <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0">
                                <tr>
                                    <td align="center" style="padding: 10px 0 30px;">
                                        <a href="%s" style="display: inline-block; background: linear-gradient(135deg, #7c3aed 0%%, #4f46e5 100%%); color: #ffffff; text-decoration: none; padding: 14px 40px; border-radius: 8px; font-size: 16px; font-weight: 600; box-shadow: 0 4px 12px rgba(124, 58, 237, 0.3);">
                                            Verify Email Address
                                        </a>
                                    </td>
                                </tr>
                            </table>

                            <!-- Alternative Link -->
                            <p style="margin: 20px 0 0; padding: 15px; background-color: #1a1a1a; border-radius: 6px; color: #888888; font-size: 12px; line-height: 1.6; border: 1px solid #333333;">
                                <strong>Or copy and paste this link:</strong><br>
                                <a href="%s" style="color: #a78bfa; word-break: break-all; text-decoration: none;">%s</a>
                            </p>

                            <!-- Expiration Notice -->
                            <p style="margin: 30px 0 0; color: #666666; font-size: 13px; line-height: 1.6; text-align: center; border-top: 1px solid #222222; padding-top: 20px;">
                                This link expires in 24 hours.
                            </p>
                        </td>
                    </tr>

                    <!-- Footer -->
                    <tr>
                        <td style="padding: 30px; text-align: center; background-color: #0a0a0a; border-radius: 0 0 12px 12px; border-top: 1px solid #222222;">
                            <p style="margin: 0; color: #555555; font-size: 12px;">
                                © 2025 Mentiq. All rights reserved.<br>
                                If you didn't create an account, you can safely ignore this email.
                            </p>
                        </td>
                    </tr>
                </table>
            </td>
        </tr>
    </table>
</body>
</html>
`, getName(toName), verifyURL, verifyURL, verifyURL)

	plainTextContent := fmt.Sprintf(`
Verify Your Email Address - Mentiq

Hi%s,

Thanks for signing up for Mentiq! Please verify your email address to activate your account.

Click the link below to verify your email:
%s

This link expires in 24 hours.

If you didn't create an account, you can safely ignore this email.
`, getName(toName), verifyURL)

	return es.sendEmail(toEmail, toName, subject, htmlContent, plainTextContent)
}

// SendPasswordResetEmail sends a password reset email
func (es *EmailService) SendPasswordResetEmail(toEmail, toName, token string) error {
	log.Printf("Preparing to send password reset email to %s", toEmail)

	if es.apiKey == "" {
		log.Println("Resend not configured, skipping email send")
		log.Printf("Would have sent password reset email to %s with token %s", toEmail, token)
		return nil
	}

	resetURL := fmt.Sprintf("%s/reset-password?token=%s", es.baseURL, token)

	subject := "Reset your password - Mentiq"

	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin: 0; padding: 0; font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; background-color: #000000; color: #ffffff;">
    <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0" style="background-color: #000000;">
        <tr>
            <td align="center" style="padding: 40px 0;">
                <table role="presentation" width="600" cellspacing="0" cellpadding="0" border="0" style="background-color: #111111; border-radius: 12px; border: 1px solid #333333; box-shadow: 0 4px 20px rgba(0,0,0,0.5);">
                    <!-- Header -->
                    <tr>
                        <td style="padding: 40px 40px 30px; text-align: center; border-bottom: 1px solid #222222;">
                            <h1 style="margin: 0; color: #ffffff; font-size: 28px; font-weight: 700; letter-spacing: -0.5px;">Mentiq</h1>
                        </td>
                    </tr>

                    <!-- Body -->
                    <tr>
                        <td style="padding: 40px;">
                            <h2 style="margin: 0 0 20px; color: #ffffff; font-size: 24px; font-weight: 600;">Reset Your Password</h2>
                            
                            <p style="margin: 0 0 20px; color: #cccccc; font-size: 16px; line-height: 1.6;">
                                Hi%s,
                            </p>

                            <p style="margin: 0 0 20px; color: #cccccc; font-size: 16px; line-height: 1.6;">
                                We received a request to reset your password for your <strong>Mentiq</strong> account. Click the button below to create a new password.
                            </p>

                            <p style="margin: 0 0 30px; color: #999999; font-size: 14px; line-height: 1.6;">
                                If you didn't request a password reset, you can safely ignore this email.
                            </p>

                            <!-- CTA Button -->
                            <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0">
                                <tr>
                                    <td align="center" style="padding: 10px 0 30px;">
                                        <a href="%s" style="display: inline-block; background: linear-gradient(135deg, #7c3aed 0%%, #4f46e5 100%%); color: #ffffff; text-decoration: none; padding: 14px 40px; border-radius: 8px; font-size: 16px; font-weight: 600; box-shadow: 0 4px 12px rgba(124, 58, 237, 0.3);">
                                            Reset Password
                                        </a>
                                    </td>
                                </tr>
                            </table>

                            <!-- Alternative Link -->
                            <p style="margin: 20px 0 0; padding: 15px; background-color: #1a1a1a; border-radius: 6px; color: #888888; font-size: 12px; line-height: 1.6; border: 1px solid #333333;">
                                <strong>Or copy and paste this link:</strong><br>
                                <a href="%s" style="color: #a78bfa; word-break: break-all; text-decoration: none;">%s</a>
                            </p>

                            <!-- Expiration Notice -->
                            <p style="margin: 30px 0 0; color: #666666; font-size: 13px; line-height: 1.6; text-align: center; border-top: 1px solid #222222; padding-top: 20px;">
                                This link expires in 1 hour for security reasons.
                            </p>
                        </td>
                    </tr>

                    <!-- Footer -->
                    <tr>
                        <td style="padding: 30px; text-align: center; background-color: #0a0a0a; border-radius: 0 0 12px 12px; border-top: 1px solid #222222;">
                            <p style="margin: 0; color: #555555; font-size: 12px;">
                                © 2025 Mentiq. All rights reserved.<br>
                                If you didn't request this password reset, please ignore this email.
                            </p>
                        </td>
                    </tr>
                </table>
            </td>
        </tr>
    </table>
</body>
</html>
`, getName(toName), resetURL, resetURL, resetURL)

	plainTextContent := fmt.Sprintf(`
Reset Your Password - Mentiq

Hi%s,

We received a request to reset your password for your Mentiq account.

Click the link below to create a new password:
%s

This link expires in 1 hour for security reasons.

If you didn't request this password reset, please ignore this email.
`, getName(toName), resetURL)

	return es.sendEmail(toEmail, toName, subject, htmlContent, plainTextContent)
}

// SendWaitlistEmail sends a confirmation email when someone joins the waitlist
func (es *EmailService) SendWaitlistEmail(toEmail, toName, unsubscribeToken string) error {
	log.Printf("Sending waitlist confirmation email to %s", toEmail)

	if es.apiKey == "" {
		log.Println("Resend not configured, skipping waitlist email")
		return nil
	}

	unsubscribeURL := fmt.Sprintf("%s/api/v1/unsubscribe?token=%s", es.baseURL, unsubscribeToken)
	subject := "Welcome to the Mentiq Waitlist! 🎉"

	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin: 0; padding: 0; font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; background-color: #f8f9fa; color: #1a1a1a;">
    <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0" style="background-color: #f8f9fa;">
        <tr>
            <td align="center" style="padding: 40px 20px;">
                <table role="presentation" width="600" cellspacing="0" cellpadding="0" border="0" style="background-color: #ffffff; border-radius: 16px; border: 1px solid #e5e7eb; box-shadow: 0 4px 24px rgba(0,0,0,0.08);">
                    <!-- Header -->
                    <tr>
                        <td style="padding: 40px 40px 30px; text-align: center; border-bottom: 1px solid #f0f0f0;">
                            <h1 style="margin: 0; color: #7c3aed; font-size: 32px; font-weight: 700; letter-spacing: -0.5px;">Mentiq</h1>
                        </td>
                    </tr>
                    
                    <!-- Content -->
                    <tr>
                        <td style="padding: 40px;">
                            <h2 style="margin: 0 0 20px; color: #1a1a1a; font-size: 24px; font-weight: 600;">You're on the list%s! 🎉</h2>
                            
                            <p style="margin: 0 0 24px; font-size: 16px; line-height: 1.7; color: #4b5563;">
                                Thank you for joining the Mentiq waitlist. We're building the ultimate churn prevention platform for SaaS founders, and we're excited to have you along for the journey.
                            </p>
                            
                            <div style="background: linear-gradient(135deg, #f3e8ff, #ede9fe); border: 1px solid #c4b5fd; border-radius: 12px; padding: 24px; margin: 24px 0;">
                                <h3 style="margin: 0 0 12px; color: #7c3aed; font-size: 18px; font-weight: 600;">What to expect:</h3>
                                <ul style="margin: 0; padding: 0 0 0 20px; color: #4b5563; line-height: 1.8;">
                                    <li>Early access when we launch</li>
                                    <li>Exclusive founder pricing</li>
                                    <li>Direct input on features we build</li>
                                    <li>Priority support from day one</li>
                                </ul>
                            </div>
                            
                            <p style="margin: 24px 0 0; font-size: 16px; line-height: 1.7; color: #4b5563;">
                                We'll be in touch soon with updates on our progress. In the meantime, feel free to reply to this email if you have any questions.
                            </p>
                        </td>
                    </tr>
                    
                    <!-- Footer -->
                    <tr>
                        <td style="padding: 30px 40px; background-color: #f9fafb; border-top: 1px solid #f0f0f0; border-radius: 0 0 16px 16px; text-align: center;">
                            <p style="margin: 0 0 12px; font-size: 14px; color: #9ca3af;">
                                &copy; %d Mentiq. All rights reserved.
                            </p>
                            <a href="%s" style="font-size: 12px; color: #9ca3af; text-decoration: underline;">Unsubscribe from promotional emails</a>
                        </td>
                    </tr>
                </table>
            </td>
        </tr>
    </table>
</body>
</html>
`, getName(toName), time.Now().Year(), unsubscribeURL)

	plainTextContent := fmt.Sprintf(`
Welcome to the Mentiq Waitlist!

Hi%s,

Thank you for joining the Mentiq waitlist. We're building the ultimate churn prevention platform for SaaS founders, and we're excited to have you along for the journey.

What to expect:
- Early access when we launch
- Exclusive founder pricing
- Direct input on features we build
- Priority support from day one

We'll be in touch soon with updates on our progress. Feel free to reply to this email if you have any questions.

Best,
The Mentiq Team

---
To unsubscribe from promotional emails: %s
`, getName(toName), unsubscribeURL)

	return es.sendEmail(toEmail, toName, subject, htmlContent, plainTextContent)
}
