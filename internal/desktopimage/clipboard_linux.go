//go:build linux && cgo && !bindings

package desktopimage

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <stdint.h>

extern int desktopClipboardPending(uint64_t id);
extern void desktopClipboardBytes(uint64_t id, void *data, int length, char *error);
extern void desktopClipboardPixels(uint64_t id, void *data, int length, int width, int height, int stride, int channels);
extern void desktopClipboardFiles(uint64_t id, void *data, int length);

static void clipboard_image(GtkClipboard *clipboard, GdkPixbuf *image, gpointer user_data) {
    uint64_t id=(uint64_t)(uintptr_t)user_data;
    if (!desktopClipboardPending(id)) return;
    if (!image) { desktopClipboardBytes(id,NULL,0,NULL); return; }
    int width=gdk_pixbuf_get_width(image), height=gdk_pixbuf_get_height(image);
    int stride=gdk_pixbuf_get_rowstride(image), channels=gdk_pixbuf_get_n_channels(image);
    // Bound allocation for clipboard-only pixel formats; common encoded MIME
    // targets below preserve their original bytes without decoding/re-encoding.
    int64_t length=(int64_t)stride*(height-1)+(int64_t)width*channels;
    if (width<=0 || height<=0 || (channels!=3 && channels!=4) || length<=0 || length>256*1024*1024) {
        desktopClipboardBytes(id,NULL,0,"The clipboard image is too large or has an unsupported pixel format."); return;
    }
    desktopClipboardPixels(id,gdk_pixbuf_get_pixels(image),(int)length,width,height,stride,channels);
}

static void clipboard_contents(GtkClipboard *clipboard, GtkSelectionData *selection, gpointer user_data) {
    uint64_t id=(uint64_t)(uintptr_t)user_data;
    if (!desktopClipboardPending(id)) return;
    int length=gtk_selection_data_get_length(selection);
    if (length>32*1024*1024) { desktopClipboardBytes(id,NULL,0,"Choose an image up to 32 MiB."); return; }
    if (length>0 && gtk_selection_data_get_format(selection)==8) {
        desktopClipboardBytes(id,(void*)gtk_selection_data_get_data(selection),length,NULL); return;
    }
    gtk_clipboard_request_image(clipboard,clipboard_image,user_data);
}

static void clipboard_files(GtkClipboard *clipboard, GtkSelectionData *selection, gpointer user_data) {
    uint64_t id=(uint64_t)(uintptr_t)user_data;
    if (!desktopClipboardPending(id)) return;
    int length=gtk_selection_data_get_length(selection);
    if (length<=0 || length>1024*1024 || gtk_selection_data_get_format(selection)!=8) {
        desktopClipboardBytes(id,NULL,0,NULL); return;
    }
    desktopClipboardFiles(id,(void*)gtk_selection_data_get_data(selection),length);
}

static void clipboard_targets(GtkClipboard *clipboard, GdkAtom *atoms, gint count, gpointer user_data) {
    uint64_t id=(uint64_t)(uintptr_t)user_data;
    if (!desktopClipboardPending(id)) return;
    const char *preferred[]={"image/png","image/jpeg","image/gif","image/webp","image/bmp","image/x-bmp","image/x-MS-bmp"};
    for (int p=0;p<7;p++) {
        GdkAtom target=gdk_atom_intern_static_string(preferred[p]);
        for (int i=0;i<count;i++) if (atoms[i]==target) {
            gtk_clipboard_request_contents(clipboard,target,clipboard_contents,user_data); return;
        }
    }
    const char *files[]={"text/uri-list","x-special/gnome-copied-files"};
    for (int p=0;p<2;p++) {
        GdkAtom target=gdk_atom_intern_static_string(files[p]);
        for (int i=0;i<count;i++) if (atoms[i]==target) {
            gtk_clipboard_request_contents(clipboard,target,clipboard_files,user_data); return;
        }
    }
    gtk_clipboard_request_image(clipboard,clipboard_image,user_data);
}

static gboolean clipboard_start(gpointer user_data) {
    uint64_t id=(uint64_t)(uintptr_t)user_data;
    if (!desktopClipboardPending(id)) return G_SOURCE_REMOVE;
    if (!gdk_display_get_default()) { desktopClipboardBytes(id,NULL,0,"The desktop clipboard is unavailable."); return G_SOURCE_REMOVE; }
    GtkClipboard *clipboard=gtk_clipboard_get(GDK_SELECTION_CLIPBOARD);
    gtk_clipboard_request_targets(clipboard,clipboard_targets,user_data);
    return G_SOURCE_REMOVE;
}

static void clipboard_request(uint64_t id) {
    // g_idle_add always dispatches on the existing GTK default main context;
    // unlike g_main_context_invoke it cannot invoke GTK on this worker thread.
    g_idle_add(clipboard_start,(gpointer)(uintptr_t)id);
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

func requestClipboard(id uint64) error { C.clipboard_request(C.uint64_t(id)); return nil }

//export desktopClipboardPending
func desktopClipboardPending(id C.uint64_t) C.int {
	if pending(uint64(id)) {
		return 1
	}
	return 0
}

//export desktopClipboardBytes
func desktopClipboardBytes(id C.uint64_t, data unsafe.Pointer, length C.int, message *C.char) {
	if !pending(uint64(id)) {
		return
	}
	result := clipboardResult{}
	if message != nil {
		result.err = errors.New(C.GoString(message))
	} else if length > 0 && length <= MaxBytes {
		result.data = C.GoBytes(data, length)
	} else {
		result.err = ErrNoImage
	}
	complete(uint64(id), result)
}

//export desktopClipboardPixels
func desktopClipboardPixels(id C.uint64_t, data unsafe.Pointer, length, width, height, stride, channels C.int) {
	if !pending(uint64(id)) {
		return
	}
	complete(uint64(id), clipboardResult{pixels: C.GoBytes(data, length), width: int(width), height: int(height), stride: int(stride), channels: int(channels)})
}

//export desktopClipboardFiles
func desktopClipboardFiles(id C.uint64_t, data unsafe.Pointer, length C.int) {
	if !pending(uint64(id)) {
		return
	}
	complete(uint64(id), clipboardResult{fileList: C.GoBytes(data, length)})
}
