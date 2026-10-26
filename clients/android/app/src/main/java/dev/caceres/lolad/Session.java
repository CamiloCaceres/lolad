package dev.caceres.lolad;

/** All credentials are scoped to one server and replaced atomically. */
final class Session {
  interface Storage {
    String get(String key);

    void save(String server, String token);

    void clear();
  }

  final Storage storage;

  Session(Storage storage) {
    this.storage = storage;
  }

  String server() {
    return storage.get("server");
  }

  String token() {
    return storage.get("token");
  }

  boolean connected() {
    return !server().isEmpty() && !token().isEmpty();
  }

  void connect(String server, String token) {
    String clean = Protocol.server(server);
    if (!token.matches("lol_[A-Za-z0-9_-]+"))
      throw new IllegalArgumentException("Paste a lol_ device token.");
    storage.save(clean, token);
  }

  void disconnect() {
    storage.clear();
  }
}
