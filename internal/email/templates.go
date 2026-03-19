package email

import "fmt"

// InviteEmail returns the HTML and plain-text bodies for an org invite.
func InviteEmail(orgName, inviteURL string) (html, text string) {
	html = fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; max-width: 560px; margin: 40px auto; color: #111;">
  <h2>You've been invited to join <strong>%s</strong> on Hizal</h2>
  <p>Click the button below to accept your invitation and create your account.</p>
  <p style="margin: 32px 0;">
    <a href="%s"
       style="background:#111;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:bold;">
      Accept Invitation
    </a>
  </p>
  <p style="color:#666;font-size:13px;">
    This link expires in 48 hours. If you didn't expect this invitation, you can safely ignore this email.
  </p>
  <hr style="border:none;border-top:1px solid #eee;margin:32px 0;" />
  <p style="color:#999;font-size:12px;">Hizal · AI context management · winnow.xferops.dev</p>
</body>
</html>`, orgName, inviteURL)

	text = fmt.Sprintf(
		"You've been invited to join %s on Hizal.\n\nAccept your invitation: %s\n\nThis link expires in 48 hours.",
		orgName, inviteURL,
	)
	return
}

// InviteExistingUserEmail is sent when the invited email is already registered.
func InviteExistingUserEmail(orgName, loginURL string) (html, text string) {
	html = fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; max-width: 560px; margin: 40px auto; color: #111;">
  <h2>You've been added to <strong>%s</strong> on Hizal</h2>
  <p>You already have a Hizal account. Log in to access your new org.</p>
  <p style="margin: 32px 0;">
    <a href="%s"
       style="background:#111;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:bold;">
      Log In
    </a>
  </p>
  <hr style="border:none;border-top:1px solid #eee;margin:32px 0;" />
  <p style="color:#999;font-size:12px;">Hizal · AI context management · winnow.xferops.dev</p>
</body>
</html>`, orgName, loginURL)

	text = fmt.Sprintf(
		"You've been added to %s on Hizal. Log in to access it: %s",
		orgName, loginURL,
	)
	return
}
