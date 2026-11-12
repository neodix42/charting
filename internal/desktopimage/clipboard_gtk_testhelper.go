//go:build linux && cgo && !bindings && clipboardintegration

package desktopimage

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

extern void desktopClipboardTestReady(void);

typedef struct { guchar *bytes; gsize length; gchar *mime; int delay; int format; } TestClipboard;
static GMainLoop *test_loop;
static void test_contents(GtkClipboard *clipboard, GtkSelectionData *selection, guint info, gpointer data) {
    TestClipboard *image=(TestClipboard*)data;
    if (image->delay) g_usleep(image->delay);
    gtk_selection_data_set(selection,gdk_atom_intern(image->mime,FALSE),image->format,image->bytes,image->length);
}
static void test_clear(GtkClipboard *clipboard,gpointer data) {
    TestClipboard *image=(TestClipboard*)data;
    g_free(image->bytes); g_free(image->mime); g_free(image);
}
static gboolean test_set(gpointer data) {
    TestClipboard *image=(TestClipboard*)data;
    GtkClipboard *clipboard=gtk_clipboard_get(GDK_SELECTION_CLIPBOARD);
    if (image->length>0) {
        GtkTargetEntry target={image->mime,0,0};
        gtk_clipboard_set_with_data(clipboard,&target,1,test_contents,test_clear,image);
    } else { gtk_clipboard_clear(clipboard); test_clear(clipboard,image); }
    desktopClipboardTestReady();
    return G_SOURCE_REMOVE;
}
static void test_publish(void *data,int length,char *mime,int delay,int format) {
    TestClipboard *image=g_new0(TestClipboard,1);
    image->bytes=g_memdup2(data,length); image->length=length; image->mime=g_strdup(mime); image->delay=delay;
    image->format=format;
    g_idle_add(test_set,image);
}
static int test_start(void) {
    if (!gtk_init_check(NULL,NULL)) return 0;
    test_loop=g_main_loop_new(NULL,FALSE);
    desktopClipboardTestReady();
    g_main_loop_run(test_loop);
    g_main_loop_unref(test_loop); test_loop=NULL;
    return 1;
}
static gboolean test_quit_idle(gpointer unused) { g_main_loop_quit(test_loop); return G_SOURCE_REMOVE; }
static void test_quit(void) { g_idle_add(test_quit_idle,NULL); }
*/
import "C"

import (
	"runtime"
	"unsafe"
)

var testReady = make(chan struct{}, 8)

//export desktopClipboardTestReady
func desktopClipboardTestReady() { testReady <- struct{}{} }

func startTestGTK() <-chan bool {
	done := make(chan bool, 1)
	go func() { runtime.LockOSThread(); defer runtime.UnlockOSThread(); done <- C.test_start() != 0 }()
	return done
}
func stopTestGTK() { C.test_quit() }
func setTestClipboard(data []byte, mime string) {
	buffer := C.CBytes(data)
	defer C.free(buffer)
	ct := C.CString(mime)
	defer C.free(unsafe.Pointer(ct))
	C.test_publish(buffer, C.int(len(data)), ct, 0, 8)
}

func setTestPixelClipboard(data []byte) {
	buffer := C.CBytes(data)
	defer C.free(buffer)
	mime := C.CString("image/png")
	defer C.free(unsafe.Pointer(mime))
	C.test_publish(buffer, C.int(len(data)), mime, 0, 16)
}
func setTestSlowClipboard(data []byte) {
	buffer := C.CBytes(data)
	defer C.free(buffer)
	mime := C.CString("image/png")
	defer C.free(unsafe.Pointer(mime))
	C.test_publish(buffer, C.int(len(data)), mime, 150000, 8)
}
