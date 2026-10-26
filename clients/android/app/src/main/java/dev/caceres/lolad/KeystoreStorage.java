package dev.caceres.lolad;

import android.content.*;
import android.security.keystore.*;
import android.util.Base64;
import java.security.KeyStore;
import javax.crypto.*;
import javax.crypto.spec.GCMParameterSpec;
import org.json.JSONObject;

/** AES-GCM ciphertext only in preferences; non-exportable key lives in Android Keystore. */
final class KeystoreStorage implements Session.Storage {
  private static final String ALIAS = "lolad.device.v1";
  private final android.content.SharedPreferences prefs;

  KeystoreStorage(Context c) {
    prefs = c.getSharedPreferences("lolad-device", Context.MODE_PRIVATE);
  }

  private javax.crypto.SecretKey key() throws Exception {
    KeyStore ks = KeyStore.getInstance("AndroidKeyStore");
    ks.load(null);
    if (!ks.containsAlias(ALIAS)) {
      KeyGenerator g = KeyGenerator.getInstance("AES", "AndroidKeyStore");
      g.init(
          new KeyGenParameterSpec.Builder(
                  ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
              .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
              .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
              .build());
      g.generateKey();
    }
    return (javax.crypto.SecretKey) ks.getKey(ALIAS, null);
  }

  public String get(String field) {
    try {
      String value = prefs.getString("sealed", "");
      if (value.isEmpty()) return "";
      String[] pieces = value.split(":");
      Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
      c.init(
          Cipher.DECRYPT_MODE,
          key(),
          new GCMParameterSpec(128, Base64.decode(pieces[0], Base64.NO_WRAP)));
      return new JSONObject(
              new String(
                  c.doFinal(Base64.decode(pieces[1], Base64.NO_WRAP)),
                  java.nio.charset.StandardCharsets.UTF_8))
          .optString(field);
    } catch (Exception e) {
      clear();
      return "";
    }
  }

  public void save(String server, String token) {
    try {
      Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
      c.init(Cipher.ENCRYPT_MODE, key());
      byte[] bytes =
          new JSONObject()
              .put("server", server)
              .put("token", token)
              .toString()
              .getBytes(java.nio.charset.StandardCharsets.UTF_8);
      String sealed =
          Base64.encodeToString(c.getIV(), Base64.NO_WRAP)
              + ":"
              + Base64.encodeToString(c.doFinal(bytes), Base64.NO_WRAP);
      if (!prefs.edit().putString("sealed", sealed).commit()) throw new IllegalStateException();
    } catch (Exception e) {
      throw new IllegalStateException("Couldn’t securely save this device. Pair again.");
    }
  }

  public void clear() {
    prefs.edit().clear().commit();
  }
}
