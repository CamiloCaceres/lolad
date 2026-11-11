package dev.caceres.lolad;

import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.TimeZone;

/**
 * Attachment rules that are identical on both clients and cheap to test: the
 * per-message limit, pasted-image naming, derived subjects, display sizes and
 * the short messages the server's status codes turn into.
 */
final class Attachments {
  static final int MAX_FILES = 10;
  static final long MAX_FILE_BYTES = 500L * 1024 * 1024;

  private Attachments() {}

  /** A peer of the form name@other.lolad is hosted elsewhere and cannot take files. */
  static boolean isRemotePeer(String peer) {
    return peer != null && peer.indexOf('@') > 0;
  }

  static String pastedName(long millis) {
    return pastedName(millis, TimeZone.getDefault());
  }

  static String pastedName(long millis, TimeZone zone) {
    SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd HH.mm.ss", Locale.US);
    format.setTimeZone(zone);
    return "Pasted image " + format.format(new Date(millis)) + ".png";
  }

  /** The server requires a subject even when the body is empty. */
  static String derivedSubject(List<String> filenames) {
    if (filenames == null || filenames.isEmpty()) return "Message";
    if (filenames.size() == 1) {
      String only = safe(filenames.get(0));
      return only.isEmpty() ? "Attachment" : only;
    }
    return filenames.size() + " files";
  }

  static String humanSize(long bytes) {
    if (bytes < 0) return "";
    if (bytes < 1024) return bytes + " B";
    double kb = bytes / 1024.0;
    if (kb < 1024) return Math.round(kb) + " KB";
    double mb = kb / 1024.0;
    if (mb < 1024) return String.format(Locale.US, "%.1f MB", mb);
    return String.format(Locale.US, "%.1f GB", mb / 1024.0);
  }

  /** Middle truncation keeps both the readable start and the extension. */
  static String middleName(String name, int max) {
    String value = name == null ? "" : name.trim();
    if (value.length() <= max) return value;
    if (max < 8) return value.substring(0, Math.max(1, max));
    int dot = value.lastIndexOf('.');
    String tail = dot > 0 && value.length() - dot <= 12 ? value.substring(dot) : "";
    int tailLength = Math.min(tail.length(), max / 3);
    int headLength = max - tailLength - 1;
    if (headLength < 4) headLength = Math.max(1, max - 2);
    String head = value.substring(0, headLength);
    String end = tailLength > 0 ? tail.substring(tail.length() - tailLength) : value.substring(value.length() - 2);
    return head + "…" + end;
  }

  static boolean imageType(String contentType) {
    if (contentType == null) return false;
    String type = contentType.toLowerCase(Locale.ROOT).split(";")[0].trim();
    return type.equals("image/png")
        || type.equals("image/jpeg")
        || type.equals("image/jpg")
        || type.equals("image/gif")
        || type.equals("image/webp");
  }

  static String extensionLabel(String filename) {
    if (filename == null) return "";
    int dot = filename.lastIndexOf('.');
    if (dot < 0 || dot == filename.length() - 1) return "";
    String extension = filename.substring(dot + 1).toUpperCase(Locale.ROOT);
    return extension.length() > 4 ? extension.substring(0, 4) : extension;
  }

  /** A clipboard image keeps a generated name only when it has no usable one. */
  static String pickName(String displayName, String contentType, long now) {
    String name = safe(displayName);
    if (!name.isEmpty() && name.indexOf('.') > 0) return name;
    if (imageType(contentType)) return pastedName(now);
    return name.isEmpty() ? "Attachment" : name;
  }

  static String safe(String value) {
    if (value == null) return "";
    StringBuilder out = new StringBuilder(value.length());
    for (int i = 0; i < value.length() && out.length() < 256; i++) {
      char c = value.charAt(i);
      if (c == '\r' || c == '\n' || c == '\t') out.append(' ');
      else if (c >= 0x20 && c != 0x7F) out.append(c);
    }
    return out.toString().trim();
  }

  /** Server status codes for an upload, in words a reader can act on. */
  static String uploadError(int status) {
    switch (status) {
      case 403:
        return "This server’s policy doesn’t allow attachments.";
      case 404:
        return "That agent isn’t available for attachments.";
      case 413:
        return "That file is larger than the 500 MB limit.";
      case 422:
        return "The server refused that file. Try another one.";
      case 401:
        return "This device was revoked — pair again.";
      default:
        return "Upload failed. Tap Retry.";
    }
  }

  static String downloadError(int status) {
    switch (status) {
      case 401:
        return "This device was revoked — pair again.";
      case 404:
        return "That file is no longer available.";
      case 422:
        return "That file can’t be downloaded for this conversation.";
      default:
        return "Download failed. Try again.";
    }
  }

  static String sendError(int status) {
    switch (status) {
      case 413:
        return "Attachments are too large to send.";
      case 422:
        return "Those files expired or were already sent. Remove them and attach again.";
      default:
        return uploadError(status);
    }
  }
}
