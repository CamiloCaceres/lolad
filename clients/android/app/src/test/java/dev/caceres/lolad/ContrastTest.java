package dev.caceres.lolad;

import static org.junit.Assert.*;

import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.HashMap;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.junit.Test;

/**
 * The palette is checked by computing WCAG contrast from the shipped XML, so a
 * later colour tweak cannot quietly drop a label below AA. Text pairs must reach
 * 4.5:1 (7:1 for terminal output); control outlines are non-text and need 3:1.
 */
public class ContrastTest {
  private static final Pattern COLOR =
      Pattern.compile("<color name=\"([a-z_]+)\">#([0-9A-Fa-f]{6})</color>");

  @Test
  public void lightAndNightPalettesMeetAa() throws Exception {
    File res = resDirectory();
    Map<String, Integer> light = colors(new File(res, "values/colors.xml"));
    Map<String, Integer> dark = colors(new File(res, "values-night/colors.xml"));
    assertFalse(light.isEmpty());
    assertFalse(dark.isEmpty());
    verify(light, "light");
    verify(dark, "night");
  }

  private void verify(Map<String, Integer> palette, String mode) {
    text(palette, mode, "ink", "paper");
    text(palette, mode, "ink", "backdrop");
    text(palette, mode, "secondary", "paper");
    text(palette, mode, "secondary", "backdrop");
    text(palette, mode, "muted", "paper");
    text(palette, mode, "muted", "backdrop");
    text(palette, mode, "muted", "quiet");
    text(palette, mode, "muted", "raised");
    text(palette, mode, "on_quiet", "quiet");
    text(palette, mode, "awake", "paper");
    text(palette, mode, "attention", "paper");
    text(palette, mode, "attention", "quiet");
    text(palette, mode, "attention", "raised");
    text(palette, mode, "error", "paper");
    text(palette, mode, "error", "raised");
    text(palette, mode, "paper", "ink");
    text(palette, mode, "paper", "attention");
    text(palette, mode, "on_mine", "mine");
    text(palette, mode, "on_mine", "card_mine");
    text(palette, mode, "ink", "quiet");
    // Terminal output is small monospace: aim well past AA.
    atLeast(palette, mode, "terminal_ink", "terminal", 7.0);
    // Non-text control boundaries.
    atLeast(palette, mode, "outline", "paper", 3.0);
    atLeast(palette, mode, "outline", "quiet", 3.0);
    atLeast(palette, mode, "outline", "raised", 3.0);
  }

  private void text(Map<String, Integer> palette, String mode, String fg, String bg) {
    atLeast(palette, mode, fg, bg, 4.5);
  }

  private void atLeast(
      Map<String, Integer> palette, String mode, String fg, String bg, double minimum) {
    Integer foreground = palette.get(fg);
    Integer background = palette.get(bg);
    assertNotNull(mode + ": missing color " + fg, foreground);
    assertNotNull(mode + ": missing color " + bg, background);
    double ratio = ratio(foreground, background);
    assertTrue(
        String.format("%s: %s on %s is %.2f:1, needs %.1f:1", mode, fg, bg, ratio, minimum),
        ratio >= minimum);
  }

  static double ratio(int first, int second) {
    double a = luminance(first);
    double b = luminance(second);
    double lighter = Math.max(a, b);
    double darker = Math.min(a, b);
    return (lighter + 0.05) / (darker + 0.05);
  }

  static double luminance(int color) {
    return channel(red(color)) * 0.2126
        + channel(green(color)) * 0.7152
        + channel(blue(color)) * 0.0722;
  }

  private static double channel(int value) {
    double c = value / 255.0;
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  }

  private static int red(int color) {
    return (color >> 16) & 0xFF;
  }

  private static int green(int color) {
    return (color >> 8) & 0xFF;
  }

  private static int blue(int color) {
    return color & 0xFF;
  }

  static Map<String, Integer> colors(File file) throws IOException {
    assertTrue("missing " + file, file.isFile());
    String source = new String(Files.readAllBytes(file.toPath()), StandardCharsets.UTF_8);
    Map<String, Integer> palette = new HashMap<>();
    Matcher matcher = COLOR.matcher(source);
    while (matcher.find()) {
      String name = matcher.group(1);
      if (name.startsWith("lolad_")) name = name.substring("lolad_".length());
      palette.put(name, Integer.parseInt(matcher.group(2), 16));
    }
    return palette;
  }

  static File resDirectory() {
    File[] candidates = {
      new File("src/main/res"), new File("app/src/main/res")
    };
    for (File candidate : candidates) if (candidate.isDirectory()) return candidate;
    fail("could not find res/ from " + new File(".").getAbsolutePath());
    return null;
  }
}
