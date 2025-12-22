package main

import (
	"fmt"
	"log"
	"os"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

// EmailService handles sending emails via SendGrid
type EmailService struct {
	apiKey    string
	fromEmail string
	fromName  string
	baseURL   string
}

// NewEmailService creates a new email service instance
func NewEmailService() *EmailService {
	return &EmailService{
		apiKey:    os.Getenv("SENDGRID_API_KEY"),
		fromEmail: os.Getenv("EMAIL_FROM_ADDRESS"),
		fromName:  os.Getenv("EMAIL_FROM_NAME"),
		baseURL:   os.Getenv("FRONTEND_URL"),
	}
}

// SendInvitationEmail sends a team invitation email
func (es *EmailService) SendInvitationEmail(toEmail, toName, inviterName, accountName, token string) error {
	log.Printf("Preparing to send invitation email to %s (Name: %s) for account %s initiated by %s", toEmail, toName, accountName, inviterName)

	if es.apiKey == "" {
		log.Println("SendGrid not configured, skipping email send")
		log.Printf("Would have sent invitation to %s from %s for account %s with token %s", toEmail, inviterName, accountName, token)
		return nil // Don't fail if SendGrid is not configured
	}

	acceptURL := fmt.Sprintf("%s/accept-invitation?token=%s", es.baseURL, token)

	subject := fmt.Sprintf("%s invited you to join %s on Mentiq", inviterName, accountName)

	// Dark theme email template
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

	from := mail.NewEmail(es.fromName, es.fromEmail)
	to := mail.NewEmail(toName, toEmail)
	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)

	log.Printf("Sending email via SendGrid to %s...", toEmail)
	client := sendgrid.NewSendClient(es.apiKey)
	response, err := client.Send(message)

	if err != nil {
		log.Printf("Error sending email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %w", err)
	}

	if response.StatusCode >= 400 {
		log.Printf("SendGrid API error for %s: Status %d, Body: %s", toEmail, response.StatusCode, response.Body)
		return fmt.Errorf("email service error: status code %d - %s", response.StatusCode, response.Body)
	}

	log.Printf("Invitation email sent successfully to %s. Status Code: %d", toEmail, response.StatusCode)
	return nil
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
		log.Println("SendGrid not configured, skipping email send")
		log.Printf("Would have sent verification email to %s with token %s", toEmail, token)
		return nil // Don't fail if SendGrid is not configured
	}

	verifyURL := fmt.Sprintf("%s/verify-email?token=%s", es.baseURL, token)

	subject := "Verify your email address - Mentiq"

	// Dark theme email template
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

	from := mail.NewEmail(es.fromName, es.fromEmail)
	to := mail.NewEmail(toName, toEmail)
	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)

	log.Printf("Sending verification email via SendGrid to %s...", toEmail)
	client := sendgrid.NewSendClient(es.apiKey)
	response, err := client.Send(message)

	if err != nil {
		log.Printf("Error sending verification email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %w", err)
	}

	if response.StatusCode >= 400 {
		log.Printf("SendGrid API error for %s: Status %d, Body: %s", toEmail, response.StatusCode, response.Body)
		return fmt.Errorf("email service error: status code %d - %s", response.StatusCode, response.Body)
	}

	log.Printf("Verification email sent successfully to %s. Status Code: %d", toEmail, response.StatusCode)
	return nil
}

// SendPasswordResetEmail sends a password reset email
func (es *EmailService) SendPasswordResetEmail(toEmail, toName, token string) error {
	log.Printf("Preparing to send password reset email to %s", toEmail)

	if es.apiKey == "" {
		log.Println("SendGrid not configured, skipping email send")
		log.Printf("Would have sent password reset email to %s with token %s", toEmail, token)
		return nil // Don't fail if SendGrid is not configured
	}

	resetURL := fmt.Sprintf("%s/reset-password?token=%s", es.baseURL, token)

	subject := "Reset your password - Mentiq"

	// Dark theme email template
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

	from := mail.NewEmail(es.fromName, es.fromEmail)
	to := mail.NewEmail(toName, toEmail)
	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)

	log.Printf("Sending password reset email via SendGrid to %s...", toEmail)
	client := sendgrid.NewSendClient(es.apiKey)
	response, err := client.Send(message)

	if err != nil {
		log.Printf("Error sending password reset email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %w", err)
	}

	if response.StatusCode >= 400 {
		log.Printf("SendGrid API error for %s: Status %d, Body: %s", toEmail, response.StatusCode, response.Body)
		return fmt.Errorf("email service error: status code %d - %s", response.StatusCode, response.Body)
	}

	log.Printf("Password reset email sent successfully to %s. Status Code: %d", toEmail, response.StatusCode)
	return nil
}
