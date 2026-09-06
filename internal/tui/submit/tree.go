package submit

import "github.com/pulseaiclub/phi/internal/tui/controller"

func (s *Submitter) StopForTree() <-chan struct{} {
	if s.resolvePermission != nil && s.permissionActive != nil && s.permissionActive() {
		s.resolvePermission(controller.AskReply{})
	}
	if s.resolveContinue != nil && s.continueActive != nil && s.continueActive() {
		s.resolveContinue(controller.ContinueReply{})
	}
	if s.resolveConfirm != nil && s.confirmActive != nil && s.confirmActive() {
		s.resolveConfirm(controller.ExtConfirmReply{})
	}
	if s.bash != nil {
		return s.bash.stopAndWait()
	}
	return nil
}
