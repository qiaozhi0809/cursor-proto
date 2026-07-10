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
// All the plugin's actual logic lives in the sibling `kernel`
// package. main.go is a thin cgo shim that:
//
//   - stores the host callback pointer for host.stream.emit /
//     host.stream.close reflected calls,
//   - marshals bytes across the C ABI boundary, and
//   - delegates dispatch to kernel.Dispatch.
//
// Splitting the code this way lets tests and the E2E harness import
// the kernel package directly (Go's runtime blocks two Go binaries
// from sharing a heap, so dlopen'ing this .dylib from another Go
// binary is not viable — see cmd/plugin-e2e/main.go).
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
	"fmt"
	"unsafe"

	"github.com/router-for-me/cursor-proto/plugin/cursor/kernel"
)

// abiVersion matches CPA's pluginabi.ABIVersion. Kept in sync manually
// so this plugin does not need to import CPA's module.
const abiVersion uint32 = 1

// init wires the cgo host callback into the kernel so the kernel's
// streaming handlers can emit chunks without knowing anything about
// C. Tests replace the callback per-dispatch via kernel.Dispatch's
// emitter argument.
func init() {
	kernel.SetHostInvoker(cgoHostInvoke)
}

// cgoHostInvoke calls the host through the stored C API. Returns
// the response bytes or an error.
func cgoHostInvoke(method string, payload []byte) ([]byte, error) {
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var cPayload *C.uint8_t
	if len(payload) > 0 {
		cPayload = (*C.uint8_t)(C.CBytes(payload))
		defer C.free(unsafe.Pointer(cPayload))
	}
	var buf C.cliproxy_buffer
	rc := C.call_host_api(cMethod, cPayload, C.size_t(len(payload)), &buf)
	var out []byte
	if buf.ptr != nil && buf.len > 0 {
		out = C.GoBytes(buf.ptr, C.int(buf.len))
	}
	if buf.ptr != nil {
		C.free_host_buffer(buf.ptr, buf.len)
	}
	if rc != 0 {
		return out, fmt.Errorf("host call %s returned %d: %s", method, int(rc), string(out))
	}
	return out, nil
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
		writeResponse(response, kernel.ErrorEnvelope("invalid_method", "method is required", false))
		return 1
	}
	m := C.GoString(method)
	var payload []byte
	if request != nil && requestLen > 0 {
		payload = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, rc := kernel.Dispatch(m, payload, nil)
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
