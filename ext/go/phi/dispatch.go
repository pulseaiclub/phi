package phi

import "github.com/pulseaiclub/phi/ext/go/pxb"

const (
	maxDeferredFrames = 32
	maxDeferredBytes  = pxb.MaxPayload
	oversizeResponse  = "extension response exceeds PXB maximum payload (16 MiB)"
)

// hostCallStopped unwinds the synchronous handler to Run when a nested host
// call cannot return an answer (shutdown, fatal connection error, overflow).
// Returning a default reply would let the handler continue on a dead host.
type hostCallStopped struct{}

func (extension *ExtensionAPI) stopped() bool {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	return extension.shutdown || extension.writeErr != nil
}

func (extension *ExtensionAPI) fail(err error) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	if extension.writeErr == nil {
		extension.writeErr = err
	}
}

func (extension *ExtensionAPI) nextFrame() (pxb.Frame, error) {
	if len(extension.deferred) == 0 {
		return extension.rd.Read()
	}
	fr := extension.deferred[0]
	extension.deferred[0] = pxb.Frame{}
	extension.deferred = extension.deferred[1:]
	extension.deferredBytes -= len(fr.Body)
	return fr, nil
}

func (extension *ExtensionAPI) write(typ, flags uint16, id uint32, body []byte) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	if extension.shutdown || extension.writeErr != nil {
		return
	}
	// Replace oversized RPC responses before touching the transport: the host
	// must receive the original ID instead of waiting for a timeout.
	if len(body) > pxb.MaxPayload {
		switch typ {
		case pxb.TypeToolResult:
			body = pxb.EncodeToolResult(
				pxb.ToolResultMsg{IsError: true, Error: oversizeResponse, Content: oversizeResponse},
			)
		case pxb.TypeCommandResponse:
			body = pxb.EncodeCommandResponse(pxb.CommandResponse{Error: oversizeResponse})
		case pxb.TypeToolDetailResult:
			// Detail responses have no error field on the current wire.
			body = pxb.EncodeToolDetailResult(pxb.ToolDetailResult{Detail: oversizeResponse})
		case pxb.TypeInterceptResponse:
			body = pxb.EncodeInterceptResp(
				pxb.InterceptResp{Block: true, Cancel: true, Stop: true, Reason: oversizeResponse},
			)
		}
	}
	extension.writeErr = extension.wr.Write(typ, flags, id, body)
}
