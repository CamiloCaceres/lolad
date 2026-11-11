package dev.caceres.lolad;

import android.content.Context;
import android.content.SharedPreferences;
import android.content.res.Configuration;

/**
 * System / Light / Dark is a display preference, not a secret: it lives in an
 * ordinary SharedPreferences file. An explicit choice wraps the activity base
 * context in a night-mode configuration, so every resource, Material dialog and
 * system bar follows it; System simply keeps the device configuration.
 */
final class ThemeMode {
  static final String PREFS = "lolad.prefs";
  static final String KEY = "theme";
  static final int SYSTEM = 0, LIGHT = 1, DARK = 2;

  private ThemeMode() {}

  static int read(Context context) {
    try {
      SharedPreferences prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
      int mode = prefs.getInt(KEY, SYSTEM);
      return mode == LIGHT || mode == DARK ? mode : SYSTEM;
    } catch (Exception e) {
      return SYSTEM;
    }
  }

  static void write(Context context, int mode) {
    context
        .getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        .edit()
        .putInt(KEY, mode == LIGHT || mode == DARK ? mode : SYSTEM)
        .apply();
  }

  /** Pure so the night-bit arithmetic is unit tested. */
  static int applyNightMode(int uiMode, int mode) {
    int cleared = uiMode & ~Configuration.UI_MODE_NIGHT_MASK;
    if (mode == DARK) return cleared | Configuration.UI_MODE_NIGHT_YES;
    if (mode == LIGHT) return cleared | Configuration.UI_MODE_NIGHT_NO;
    return uiMode;
  }

  static Context wrap(Context base, int mode) {
    if (mode != LIGHT && mode != DARK) return base;
    Configuration configuration = new Configuration(base.getResources().getConfiguration());
    configuration.uiMode = applyNightMode(configuration.uiMode, mode);
    return base.createConfigurationContext(configuration);
  }

  static boolean isDark(Context context) {
    return (context.getResources().getConfiguration().uiMode
            & Configuration.UI_MODE_NIGHT_MASK)
        == Configuration.UI_MODE_NIGHT_YES;
  }

  static String label(int mode) {
    return mode == LIGHT ? "Light" : mode == DARK ? "Dark" : "System";
  }
}
