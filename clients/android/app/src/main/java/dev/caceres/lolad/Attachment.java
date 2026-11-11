package dev.caceres.lolad;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * One file carried by a mail (or, while unsent, one local upload). Immutable
 * metadata only: bytes are never held here. {@code localUri} is set on upload
 * chips and optimistic messages so their thumbnail can come from the device.
 */
final class Attachment {
  final String id, filename, contentType, digest, scanStatus, localUri;
  final long size;

  Attachment(
      String id,
      String filename,
      String contentType,
      long size,
      String digest,
      String scanStatus,
      String localUri) {
    this.id = id == null ? "" : id;
    this.filename = filename == null || filename.isEmpty() ? "Attachment" : filename;
    this.contentType = contentType == null ? "" : contentType;
    this.size = Math.max(0, size);
    this.digest = digest == null ? "" : digest;
    this.scanStatus = scanStatus == null ? "" : scanStatus;
    this.localUri = localUri;
  }

  static Attachment from(JSONObject json) {
    if (json == null) return null;
    String id = json.optString("id");
    if (id.isEmpty()) return null;
    return new Attachment(
        id,
        json.optString("filename"),
        json.optString("content_type"),
        json.optLong("size"),
        json.optString("digest"),
        json.optString("scan_status", "basic_clean"),
        null);
  }

  static List<Attachment> list(JSONArray array) {
    if (array == null || array.length() == 0) return Collections.emptyList();
    List<Attachment> out = new ArrayList<>(array.length());
    for (int i = 0; i < array.length(); i++) {
      Attachment attachment = from(array.optJSONObject(i));
      if (attachment != null) out.add(attachment);
    }
    return out.isEmpty() ? Collections.emptyList() : Collections.unmodifiableList(out);
  }

  /** A local-only card for an optimistic message, before the server answers. */
  static Attachment pending(String filename, String contentType, long size, String localUri) {
    return new Attachment("", filename, contentType, size, "", "basic_clean", localUri);
  }

  boolean isImage() {
    return Attachments.imageType(contentType);
  }

  boolean suspicious() {
    return "suspicious".equals(scanStatus);
  }

  /** inline=1 is honoured only for clean png/jpeg/gif/webp; anything else downloads. */
  boolean canInline() {
    return isImage() && !suspicious();
  }

  String sizeLabel() {
    return Attachments.humanSize(size);
  }

  String shortName() {
    return Attachments.middleName(filename, 30);
  }

  String badge() {
    return Attachments.extensionLabel(filename);
  }

  String cacheKey() {
    return id.isEmpty() ? "local:" + localUri : "att:" + id;
  }

  @Override
  public boolean equals(Object other) {
    if (this == other) return true;
    if (!(other instanceof Attachment)) return false;
    Attachment that = (Attachment) other;
    return size == that.size
        && id.equals(that.id)
        && filename.equals(that.filename)
        && contentType.equals(that.contentType)
        && scanStatus.equals(that.scanStatus)
        && (localUri == null ? that.localUri == null : localUri.equals(that.localUri));
  }

  @Override
  public int hashCode() {
    int result = id.hashCode();
    result = 31 * result + filename.hashCode();
    result = 31 * result + (int) (size ^ (size >>> 32));
    result = 31 * result + contentType.hashCode();
    return result;
  }

  @Override
  public String toString() {
    // Filenames are user data; this stays deliberately free of ids and paths.
    return "Attachment(" + contentType + ", " + size + " bytes)";
  }
}
