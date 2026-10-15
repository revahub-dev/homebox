package parsers

import (
	"regexp"
	"strings"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/mailmsg"
)

var reConfirmSubject = regexp.MustCompile(`(?i)forwarding confirmation|confirm.*forward|転送.*確認|確認.*転送|自動転送`)

// IsForwardingConfirmation reports whether the message looks like a mail
// provider asking to confirm an auto-forward address (Gmail, Outlook, iCloud).
func IsForwardingConfirmation(m *mailmsg.Message) bool {
	if reConfirmSubject.MatchString(m.Subject) {
		return true
	}
	from := m.OriginalFrom
	return strings.HasPrefix(from, "forwarding-noreply@") && strings.Contains(m.Text, "forward")
}
