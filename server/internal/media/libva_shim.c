// Generated forwarders — see libva_shim.go for why they exist and how the
// uniform 12-argument signature is ABI-safe. Regenerate rather than edit.

#include <dlfcn.h>
#include <stddef.h>

// Every libva entry point ffmpeg references takes only pointer- and
// integer-class arguments (verified: no float, double, or by-value struct
// parameters anywhere in the set), so on the SysV x86-64 ABI they all pass
// in the same registers and stack slots as void *. One signature therefore
// forwards all of them.
typedef void *(*va_fn)(void *, void *, void *, void *, void *, void *,
                       void *, void *, void *, void *, void *, void *);

static void *va_lib;      // libva.so.2
static void *va_drm_lib;  // libva-drm.so.2, which holds vaGetDisplayDRM
static int va_loaded;

static void va_load(void) {
    if (va_loaded) return;
    va_loaded = 1;
    // RTLD_LOCAL: these symbols must not leak into the global namespace and
    // shadow the forwarders below.
    va_lib = dlopen("libva.so.2", RTLD_LAZY | RTLD_LOCAL);
    va_drm_lib = dlopen("libva-drm.so.2", RTLD_LAZY | RTLD_LOCAL);
}

// cineva_libva_present reports whether the host has a usable libva.
int cineva_libva_present(void) {
    va_load();
    return va_lib != NULL && va_drm_lib != NULL;
}

static void *va_sym(const char *name) {
    va_load();
    void *p = va_lib ? dlsym(va_lib, name) : NULL;
    if (!p && va_drm_lib) p = dlsym(va_drm_lib, name);
    return p;
}

// VA_STATUS_ERROR_UNKNOWN. Returned when libva is absent so callers take
// their normal failure path; 0 would read as VA_STATUS_SUCCESS.
#define VA_SHIM_ERR ((void *)(long)0xFFFFFFFF)

#define VA_ARGS void *a0, void *a1, void *a2, void *a3, void *a4, void *a5, \
                void *a6, void *a7, void *a8, void *a9, void *a10, void *a11
#define VA_PASS a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11

// Resolves on first call and caches. A missing symbol fails like a missing
// library rather than crashing.
#define VA_FORWARD(name, fail)                       \
    void *name(VA_ARGS) {                            \
        static va_fn fn;                             \
        static int tried;                            \
        if (!tried) { tried = 1; fn = (va_fn)va_sym(#name); } \
        if (!fn) return (fail);                      \
        return fn(VA_PASS);                          \
    }

// A string accessor must hand back something safe to dereference: callers
// print the result without checking for NULL.
static const char va_unavailable[] = "libva unavailable";
#define VA_SHIM_STR ((void *)va_unavailable)

// Status-returning entry points.
VA_FORWARD(vaBeginPicture, VA_SHIM_ERR)
VA_FORWARD(vaBufferInfo, VA_SHIM_ERR)
VA_FORWARD(vaCreateBuffer, VA_SHIM_ERR)
VA_FORWARD(vaCreateConfig, VA_SHIM_ERR)
VA_FORWARD(vaCreateContext, VA_SHIM_ERR)
VA_FORWARD(vaCreateImage, VA_SHIM_ERR)
VA_FORWARD(vaCreateSurfaces, VA_SHIM_ERR)
VA_FORWARD(vaDeriveImage, VA_SHIM_ERR)
VA_FORWARD(vaDestroyBuffer, VA_SHIM_ERR)
VA_FORWARD(vaDestroyConfig, VA_SHIM_ERR)
VA_FORWARD(vaDestroyContext, VA_SHIM_ERR)
VA_FORWARD(vaDestroyImage, VA_SHIM_ERR)
VA_FORWARD(vaDestroySurfaces, VA_SHIM_ERR)
VA_FORWARD(vaEndPicture, VA_SHIM_ERR)
VA_FORWARD(vaGetConfigAttributes, VA_SHIM_ERR)
VA_FORWARD(vaGetDisplayAttributes, VA_SHIM_ERR)
VA_FORWARD(vaGetImage, VA_SHIM_ERR)
VA_FORWARD(vaInitialize, VA_SHIM_ERR)
VA_FORWARD(vaLockSurface, VA_SHIM_ERR)
VA_FORWARD(vaMapBuffer, VA_SHIM_ERR)
VA_FORWARD(vaMapBuffer2, VA_SHIM_ERR)
VA_FORWARD(vaMaxNumEntrypoints, VA_SHIM_ERR)
VA_FORWARD(vaMaxNumImageFormats, VA_SHIM_ERR)
VA_FORWARD(vaMaxNumProfiles, VA_SHIM_ERR)
VA_FORWARD(vaPutImage, VA_SHIM_ERR)
VA_FORWARD(vaQueryConfigEntrypoints, VA_SHIM_ERR)
VA_FORWARD(vaQueryConfigProfiles, VA_SHIM_ERR)
VA_FORWARD(vaQueryImageFormats, VA_SHIM_ERR)
VA_FORWARD(vaQuerySurfaceAttributes, VA_SHIM_ERR)
VA_FORWARD(vaQueryVideoProcFilterCaps, VA_SHIM_ERR)
VA_FORWARD(vaQueryVideoProcPipelineCaps, VA_SHIM_ERR)
VA_FORWARD(vaRenderPicture, VA_SHIM_ERR)
VA_FORWARD(vaSetDriverName, VA_SHIM_ERR)
VA_FORWARD(vaSetErrorCallback, VA_SHIM_ERR)
VA_FORWARD(vaSetInfoCallback, VA_SHIM_ERR)
VA_FORWARD(vaSyncBuffer, VA_SHIM_ERR)
VA_FORWARD(vaSyncSurface, VA_SHIM_ERR)
VA_FORWARD(vaTerminate, VA_SHIM_ERR)
VA_FORWARD(vaUnlockSurface, VA_SHIM_ERR)
VA_FORWARD(vaUnmapBuffer, VA_SHIM_ERR)

// Display constructors: NULL is the documented failure value.
VA_FORWARD(vaGetDisplayDRM, NULL)

// String accessors.
VA_FORWARD(vaBufferTypeStr, VA_SHIM_STR)
VA_FORWARD(vaConfigAttribTypeStr, VA_SHIM_STR)
VA_FORWARD(vaEntrypointStr, VA_SHIM_STR)
VA_FORWARD(vaErrorStr, VA_SHIM_STR)
VA_FORWARD(vaProfileStr, VA_SHIM_STR)
VA_FORWARD(vaQueryVendorString, VA_SHIM_STR)
VA_FORWARD(vaStatusStr, VA_SHIM_STR)
