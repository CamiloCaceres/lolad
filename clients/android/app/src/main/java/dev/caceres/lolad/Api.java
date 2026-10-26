package dev.caceres.lolad;

import java.io.*;
import java.util.concurrent.TimeUnit;
import okhttp3.*;
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
