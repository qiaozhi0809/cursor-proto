// Cursor plugin for CLIProxyAPI (CPA).
//
// This binary exposes the CPA plugin ABI as a C-shared library:
//
//	CGO_ENABLED=1 go build -buildmode=c-shared \
//	  -o plugin/cursor/cursor.so ./plugin/cursor           (linux)
//	CGO_ENABLED=1 go build -buildmode=c-shared \
//	  -o plugin/cursor/cursor.dylib ./plugin/cursor        (macOS)
//
// The resulting shared object exports the four symbols CPA's plugin
// host expects: cliproxy_plugin_init, cliproxyPluginCall,
// cliproxyPluginFree, and cliproxyPluginShutdown. CPA dispatches every
// interaction as a JSON envelope through cliproxyPluginCall(method, ...).
//
// Scope of this pass (Phase 7f):
//
//   - plugin.register / plugin.reconfigure / plugin.shutdown lifecycle.
//   - auth.identifier + auth.parse for our on-disk "cursor" JSON shape
//     (produced by cmd/cursor-to-cpa).
//   - auth.refresh — currently a passthrough that returns the existing
//     storage; wiring up the real refresh path is a follow-up (see
//     docs/phase-7f-plugin-plan.md).
//   - executor.identifier reports the "cursor" provider.
//   - model.static returns the shipped Cursor model list so CPA's
//     scheduler can advertise them.
//   - executor.execute_stream and executor.count_tokens return an
//     explicit "not implemented" error at the moment. Both are the
//     next milestone: `docs/phase-7f-plugin-plan.md` records the exact
//     data path we need to close (protocol translation + Cursor
//     RunSSE + host stream chunk emission).
//
// The plugin intentionally does NOT depend on CPA's Go module. All the
// ABI types are declared locally so we can build against just the
// standard library plus cursor-proto's own packages, keeping the
// deliverable self-contained.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
    void* ptr;
    size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
    uint32_t abi_version;
    void* host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;
    cliproxy_plugin_free_fn free_buffer;
    cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
    stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) {
        return 1;
    }
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
        stored_host->free_buffer(ptr, len);
    }
}
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

// abiVersion matches CPA's pluginabi.ABIVersion. Kept in sync manually
// so this plugin does not need to import CPA's module.
const abiVersion uint32 = 1

// pluginName is the stable identifier reported to CPA via
// plugin.register, auth.identifier, and executor.identifier.
const pluginName = "cursor"

// envelope is the plugin-side view of pluginabi.Envelope. Duplicated
// here (rather than imported) so this binary depends only on the
// standard library + cursor-proto's own packages.
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

// main is required by go build but never executed for a c-shared library.
func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required", false))
		return 1
	}
	m := C.GoString(method)
	var payload []byte
	if request != nil && requestLen > 0 {
		payload = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, rc := dispatch(m, payload)
	writeResponse(response, raw)
	return C.int(rc)
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

// dispatch routes a plugin ABI method to its handler. It returns the
// envelope bytes to send back to the host and the plugin-call return
// code (0 on success, non-zero on error).
func dispatch(method string, payload []byte) ([]byte, int) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		return okEnvelopeJSON(registerResult()), 0
	case "plugin.shutdown":
		return okEnvelopeJSON(`{}`), 0

	case "auth.identifier":
		return okEnvelopeJSON(identifierResult()), 0
	case "auth.parse":
		raw, rc := handleAuthParse(payload)
		// Teach the pool-status registry about accounts as they arrive.
		if rc == 0 {
			var req authParseRequest
			if err := json.Unmarshal(payload, &req); err == nil {
				registerAccountFromParse(req.RawJSON)
			}
		}
		return raw, rc
	case "auth.refresh":
		return handleAuthRefresh(payload)
	case "auth.login.start":
		return errorEnvelope("not_implemented", "cursor login is performed by cursor-proto's cmd/cursor-login binary; convert its output with cursor-to-cpa", false), 1
	case "auth.login.poll":
		return errorEnvelope("not_implemented", "cursor login poll is not exposed through the plugin ABI", false), 1

	case "executor.identifier":
		return okEnvelopeJSON(identifierResult()), 0
	case "executor.execute", "executor.execute_stream":
		return errorEnvelope("not_implemented", "cursor executor streaming is a follow-up milestone; see docs/phase-7f-plugin-plan.md", true), 1
	case "executor.count_tokens":
		return errorEnvelope("not_implemented", "cursor token counting is a follow-up milestone", true), 1

	case "model.register", "model.static":
		return okEnvelopeJSON(staticModelsResult()), 0
	case "model.for_auth":
		return okEnvelopeJSON(staticModelsResult()), 0

	case "management.register":
		return okEnvelopeJSON(managementRegisterResult()), 0
	case "management.handle":
		return handleManagement(payload)

	default:
		return errorEnvelope("unknown_method", "unknown method: "+method, false), 1
	}
}

// okEnvelopeJSON wraps a pre-marshaled result JSON in an envelope.
func okEnvelopeJSON(result string) []byte {
	buf, _ := json.Marshal(envelope{OK: true, Result: json.RawMessage(result)})
	return buf
}

// errorEnvelope constructs an OK=false envelope with a code + message.
func errorEnvelope(code, message string, retryable bool) []byte {
	buf, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, Retryable: retryable}})
	return buf
}

// writeResponse copies raw into the host-owned response buffer using
// C.CBytes (the host frees it via cliproxyPluginFree).
func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
