package dev.caceres.lolad;

import java.io.*;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Consumer;
import okhttp3.*;
import okio.BufferedSink;
import org.json.*;

final class Api {
  static final MediaType JSON = MediaType.get("application/json; charset=utf-8");
  final String server, token;
  final OkHttpClient http;

  Api(String server, String token) {
    this.server = Protocol.server(server);
    this.token = token;
    http =
        new OkHttpClient.Builder()
            .connectTimeout(8, TimeUnit.SECONDS)
            .readTimeout(15, TimeUnit.SECONDS)
            .followRedirects(false)
            .followSslRedirects(false)
            .build();
  }

  /** Byte progress for streamed uploads, downloads and previews. */
  interface Progress {
    void progress(long written, long total);
  }

  interface StreamSource {
    InputStream open() throws IOException;
  }

  static final class Failure extends IOException {
    final int status;
    final long retryAfter;

    Failure(int status, String message, long retryAfter) {
      super(message);
      this.status = status;
      this.retryAfter = retryAfter;
    }
  }

  Request request(String path, JSONObject body) {
    Request.Builder r = new Request.Builder().url(server + path);
    if (!token.isEmpty()) r.header("Authorization", "Bearer " + token);
    if (body != null) r.post(RequestBody.create(body.toString(), JSON));
    return r.build();
  }

  void check(Response r) throws IOException {
    if (r.isSuccessful()) return;
    long retry = 60;
    try {
      retry = Long.parseLong(r.header("Retry-After", "60"));
    } catch (NumberFormatException ignored) {
    }
    String message;
    switch (r.code()) {
      case 401:
        message =
            token.isEmpty()
                ? "That code is invalid, expired or already used. Create a new code on the"
                      + " dashboard."
                : "This device was revoked — pair again.";
        break;
      case 429:
        message = "Too many attempts. Try again in " + retry + " seconds.";
        break;
      case 403:
        message = "The server’s policy does not allow this action.";
        break;
      case 404:
        message = "This agent or message is no longer available.";
        break;
      case 422:
        message = "The server could not accept this action. Refresh and try again.";
        break;
      default:
        message = "Server error (" + r.code() + "). Try again shortly.";
    }
    throw new Failure(r.code(), message, Math.max(1, retry));
  }

  /**
   * The exact multipart shape the server requires: one part named "file".
   * OkHttp percent-escapes quotes and line breaks in the filename.
   */
  static MultipartBody multipart(String filename, RequestBody part) {
    return new MultipartBody.Builder()
        .setType(MultipartBody.FORM)
        .addFormDataPart("file", filename, part)
        .build();
  }

  /** Streams a file from disk; the file is never read into memory as a whole. */
  static RequestBody fileBody(File file, String contentType, Progress progress) {
    long length = file == null ? 0 : file.length();
    return streamBody(() -> new FileInputStream(file), length, contentType, progress);
  }

  static RequestBody streamBody(
      StreamSource source, long length, String contentType, Progress progress) {
    MediaType type =
        contentType == null || contentType.isEmpty() ? null : MediaType.parse(contentType);
    return new RequestBody() {
      @Override
      public MediaType contentType() {
        return type;
      }

      @Override
      public long contentLength() {
        return length;
      }

      @Override
      public void writeTo(BufferedSink sink) throws IOException {
        long total = length;
        long written = 0;
        int last = -1;
        byte[] buffer = new byte[64 * 1024];
        try (InputStream input = source.open()) {
          if (input == null) throw new IOException("That file could not be opened.");
          int read;
          while ((read = input.read(buffer)) != -1) {
            sink.write(buffer, 0, read);
            written += read;
            if (progress != null) {
              int percent = total > 0 ? (int) Math.min(100, written * 100 / total) : 0;
              if (percent != last) {
                last = percent;
                progress.progress(written, total);
              }
            }
          }
        }
        if (progress != null) progress.progress(written, total > 0 ? total : written);
      }
    };
  }

  /** Uploads one file to a conversation; returns the stored attachment JSON. */
  JSONObject upload(
      String peer, String filename, String contentType, RequestBody part, AtomicReference<Call> handle)
      throws Exception {
    Request.Builder builder =
        new Request.Builder()
            .url(server + "/api/mail/attachments?peer=" + Protocol.segment(peer))
            .post(multipart(filename, part));
    if (!token.isEmpty()) builder.header("Authorization", "Bearer " + token);
    Call call = http.newBuilder().readTimeout(120, TimeUnit.SECONDS).build().newCall(builder.build());
    if (handle != null) handle.set(call);
    try (Response response = call.execute()) {
      if (!response.isSuccessful()) {
        throw new Failure(
            response.code(), Attachments.uploadError(response.code()), 60);
      }
      if (response.body() == null) throw new IOException("The server sent no attachment.");
      return new JSONObject(response.body().string());
    }
  }

  String attachmentPath(String peer, String id, boolean inline) {
    return "/api/mail/attachments/"
        + Protocol.segment(id)
        + "?peer="
        + Protocol.segment(peer)
        + (inline ? "&inline=1" : "");
  }

  /** Synchronous streamed fetch to a file, for cached inline thumbnails. */
  void fetch(String peer, String id, boolean inline, File target, Progress progress)
      throws IOException {
    Request.Builder builder = new Request.Builder().url(server + attachmentPath(peer, id, inline));
    if (!token.isEmpty()) builder.header("Authorization", "Bearer " + token);
    try (Response response = http.newCall(builder.build()).execute()) {
      if (!response.isSuccessful())
        throw new Failure(response.code(), Attachments.downloadError(response.code()), 60);
      if (response.body() == null) throw new IOException("The server sent no file.");
      long total = response.body().contentLength();
      if (total > 40L * 1024 * 1024) throw new IOException("That preview is too large.");
      File temp = new File(target.getPath() + ".part");
      byte[] buffer = new byte[64 * 1024];
      long written = 0;
      int last = -1;
      try (InputStream in = response.body().byteStream();
          OutputStream out = new FileOutputStream(temp)) {
        int read;
        while ((read = in.read(buffer)) != -1) {
          out.write(buffer, 0, read);
          written += read;
          if (progress != null) {
            int percent = total > 0 ? (int) Math.min(100, written * 100 / total) : 0;
            if (percent != last) {
              last = percent;
              progress.progress(written, total);
            }
          }
        }
      }
      if (!temp.renameTo(target)) {
        temp.delete();
        throw new IOException("Could not save that preview.");
      }
    }
  }

  /** A streamed download; images ask for inline=1 and decide what they get from Content-Type. */
  Call download(
      String peer,
      String id,
      boolean inline,
      OutputStream sink,
      Progress progress,
      Runnable success,
      Consumer<Exception> failure) {
    Request.Builder builder = new Request.Builder().url(server + attachmentPath(peer, id, inline));
    if (!token.isEmpty()) builder.header("Authorization", "Bearer " + token);
    Call call = http.newCall(builder.build());
    call.enqueue(
        new Callback() {
          @Override
          public void onFailure(Call c, IOException e) {
            closeQuietly(sink);
            if (!c.isCanceled()) failure.accept(e);
          }

          @Override
          public void onResponse(Call c, Response response) {
            try (Response r = response) {
              if (!r.isSuccessful())
                throw new Failure(r.code(), Attachments.downloadError(r.code()), 60);
              if (r.body() == null) throw new IOException("The server sent no file.");
              long total = r.body().contentLength();
              InputStream in = r.body().byteStream();
              byte[] buffer = new byte[64 * 1024];
              long written = 0;
              int last = -1;
              int read;
              while ((read = in.read(buffer)) != -1) {
                sink.write(buffer, 0, read);
                written += read;
                if (progress != null) {
                  int percent = total > 0 ? (int) Math.min(100, written * 100 / total) : 0;
                  if (percent != last) {
                    last = percent;
                    progress.progress(written, total);
                  }
                }
              }
              sink.flush();
              closeQuietly(sink);
              if (!c.isCanceled()) success.run();
            } catch (Exception e) {
              closeQuietly(sink);
              if (!c.isCanceled()) failure.accept(e);
            }
          }
        });
    return call;
  }

  private static void closeQuietly(OutputStream sink) {
    try {
      if (sink != null) sink.close();
    } catch (Exception ignored) {
    }
  }

  String call(String path, JSONObject body) throws IOException {
    try (Response r = http.newCall(request(path, body)).execute()) {
      check(r);
      if (r.body() == null) throw new IOException("Empty server response.");
      return r.body().string();
    }
  }

  JSONObject object(String path) throws Exception {
    return new JSONObject(call(path, null));
  }

  JSONArray array(String path) throws Exception {
    String body = call(path, null);
    return "null".equals(body.trim()) ? new JSONArray() : new JSONArray(body);
  }

  JSONObject post(String path, JSONObject body) throws Exception {
    return new JSONObject(call(path, body));
  }

  Call stream(
      Protocol.EventSink sink, java.util.function.Consumer<Exception> failure, Runnable connected) {
    Call call =
        http.newBuilder()
            .readTimeout(45, TimeUnit.SECONDS)
            .build()
            .newCall(request("/api/stream", null));
    call.enqueue(
        new Callback() {
          public void onFailure(Call c, IOException e) {
            if (!c.isCanceled()) failure.accept(e);
          }

          public void onResponse(Call c, Response r) {
            try (Response response = r) {
              check(response);
              if (response.body() == null
                  || !response.header("Content-Type", "").contains("text/event-stream"))
                throw new IOException("Live updates unavailable.");
              connected.run();
              Protocol.Sse parser = new Protocol.Sse(sink);
              BufferedReader reader = new BufferedReader(response.body().charStream());
              String line;
              while ((line = reader.readLine()) != null && !c.isCanceled()) parser.line(line);
              if (!c.isCanceled()) failure.accept(new IOException("Live updates disconnected."));
            } catch (Exception e) {
              if (!c.isCanceled()) failure.accept(e);
            }
          }
        });
    return call;
  }
}
