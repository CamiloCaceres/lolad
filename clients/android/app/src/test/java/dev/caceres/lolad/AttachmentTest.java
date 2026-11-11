package dev.caceres.lolad;

import static org.junit.Assert.*;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.TimeZone;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;

/** Attachment rules: naming, sizes, subjects, limits and decoding. */
public class AttachmentTest {
  @Test
  public void pastedImageNamingUsesLocalWallClock() {
    long millis = Instant.parse("2026-10-28T15:42:03Z").toEpochMilli();
    assertEquals(
        "Pasted image 2026-10-28 15.42.03.png",
        Attachments.pastedName(millis, TimeZone.getTimeZone("UTC")));
    assertEquals(
        "Pasted image 2026-10-28 16.42.03.png",
        Attachments.pastedName(millis, TimeZone.getTimeZone("Europe/Madrid")));
  }

  @Test
  public void clipboardNamesPreferARealFileName() {
    assertEquals(
        "Quarterly report.pdf",
        Attachments.pickName("Quarterly report.pdf", "application/pdf", 0L));
    assertEquals(Attachments.pastedName(0L), Attachments.pickName(null, "image/png", 0L));
    assertEquals(Attachments.pastedName(0L), Attachments.pickName("image", "image/png", 0L));
    assertEquals("Attachment", Attachments.pickName(null, "application/octet-stream", 0L));
  }

  @Test
  public void derivedSubjectUsesTheOnlyFileNameOrACount() {
    assertEquals("Quarterly report.pdf", Attachments.derivedSubject(Arrays.asList("Quarterly report.pdf")));
    assertEquals(
        "3 files",
        Attachments.derivedSubject(Arrays.asList("a.png", "b.png", "c.png")));
    assertEquals("Message", Attachments.derivedSubject(new ArrayList<String>()));
  }

  @Test
  public void limitsAreTenFilesAndFiveHundredMegabytes() throws Exception {
    assertEquals(10, Attachments.MAX_FILES);
    assertEquals(500L * 1024 * 1024, Attachments.MAX_FILE_BYTES);
    List<String> ids = new ArrayList<>();
    for (int i = 0; i < 10; i++) ids.add("att_" + i);
    try {
      Protocol.outgoing("iris", "", null, null, ids, "10 files");
      ids.add("att_10");
      Protocol.outgoing("iris", "", null, null, ids, "11 files");
      fail("eleven attachments must be refused locally");
    } catch (IllegalArgumentException expected) {
      assertTrue(expected.getMessage().contains("10"));
    }
  }

  @Test
  public void sendPayloadCarriesAttachmentIdsAndDerivedSubject() throws Exception {
    JSONObject payload =
        Protocol.outgoing(
            "iris", "", null, null, Arrays.asList("att_1"), "Quarterly report.pdf");
    assertEquals("", payload.getString("body"));
    assertEquals("Quarterly report.pdf", payload.getString("subject"));
    assertEquals("att_1", payload.getJSONArray("attachments").getString(0));
    assertEquals("say", Protocol.chat(payload).getString("kind"));
    assertEquals("request", payload.getString("type"));

    JSONObject many =
        Protocol.outgoing(
            "iris", "", null, null, Arrays.asList("a", "b", "c"), "3 files");
    assertEquals("3 files", many.getString("subject"));
    assertEquals(3, many.getJSONArray("attachments").length());

    JSONObject reply =
        Protocol.outgoing(
            "iris",
            "",
            new JSONObject().put("id", "msg_1").put("subject", "Deploy?"),
            null,
            Arrays.asList("att_1"),
            "unused.pdf");
    assertEquals("Re: Deploy?", reply.getString("subject"));
    assertEquals("msg_1", reply.getString("in_reply_to"));
    assertEquals("response", reply.getString("type"));
  }

  @Test
  public void emptyMessageWithoutFilesIsRefused() throws Exception {
    try {
      Protocol.outgoing("iris", "   ", null, null, null, "");
      fail("nothing to send");
    } catch (IllegalArgumentException expected) {
      assertTrue(expected.getMessage().contains("attach"));
    }
  }

  @Test
  public void uploadAndDownloadErrorsBecomeShortSentences() {
    assertTrue(Attachments.uploadError(403).contains("policy"));
    assertTrue(Attachments.uploadError(404).contains("agent"));
    assertTrue(Attachments.uploadError(413).contains("500 MB"));
    assertTrue(Attachments.uploadError(422).contains("refused"));
    assertTrue(Attachments.uploadError(401).contains("revoked"));
    assertTrue(Attachments.uploadError(500).contains("Retry"));
    assertTrue(Attachments.downloadError(404).contains("no longer available"));
    assertTrue(Attachments.sendError(422).contains("expired"));
  }

  @Test
  public void remotePeersAreRecognised() {
    assertFalse(Attachments.isRemotePeer("iris"));
    assertTrue(Attachments.isRemotePeer("iris@other.lolad"));
    assertFalse(Attachments.isRemotePeer(null));
  }

  @Test
  public void sizesAndNamesStayReadable() {
    assertEquals("180 KB", Attachments.humanSize(184320));
    assertEquals("1.4 MB", Attachments.humanSize(1474560));
    assertEquals("512 B", Attachments.humanSize(512));
    String shortened = Attachments.middleName("Quarterly report for the whole team.pdf", 20);
    assertTrue(shortened.length() <= 20);
    assertTrue(shortened.startsWith("Quarterly"));
    assertTrue(shortened.endsWith("pdf"));
    assertEquals("PDF", Attachments.extensionLabel("Quarterly report.pdf"));
    assertTrue(Attachments.imageType("image/png"));
    assertTrue(Attachments.imageType("IMAGE/JPEG; charset=binary"));
    assertFalse(Attachments.imageType("image/svg+xml"));
    assertFalse(Attachments.imageType("application/pdf"));
  }

  @Test
  public void mailAttachmentsAreDecodedFromTheTopLevelField() throws Exception {
    JSONArray mail = new JSONArray();
    mail.put(
        new JSONObject()
            .put("id", "msg_1")
            .put("from_name", "iris")
            .put("to_name", "milo")
            .put("subject", "Report")
            .put("body", "")
            .put("status", "unread")
            .put("created_at", "2026-10-28T09:12:04.512345Z")
            .put(
                "attachments",
                new JSONArray()
                    .put(
                        new JSONObject()
                            .put("id", "att_1")
                            .put("filename", "Quarterly report.pdf")
                            .put("content_type", "application/pdf")
                            .put("size", 184320)
                            .put("digest", "sha256:abc")
                            .put("scan_status", "basic_clean"))
                    .put(
                        new JSONObject()
                            .put("id", "att_2")
                            .put("filename", "holiday.png")
                            .put("content_type", "image/png")
                            .put("size", 2048)
                            .put("scan_status", "suspicious"))));
    List<Model.Message> messages = Model.messages(mail, "milo", "Iris");
    assertEquals(1, messages.size());
    Model.Message message = messages.get(0);
    assertEquals(2, message.attachments.size());
    Attachment report = message.attachments.get(0);
    assertEquals("att_1", report.id);
    assertEquals("application/pdf", report.contentType);
    assertEquals(184320, report.size);
    assertFalse(report.isImage());
    assertFalse(report.suspicious());
    assertEquals("PDF", report.badge());
    Attachment image = message.attachments.get(1);
    assertTrue(image.isImage());
    assertTrue(image.suspicious());
    assertFalse("a suspicious image is never inlined", image.canInline());

    assertTrue(Model.messages(new JSONArray(), "milo", "Iris").isEmpty());
  }

  @Test
  public void inboxPreviewShowsFilesWhenThereIsNoText() throws Exception {
    JSONArray conversations = new JSONArray();
    conversations.put(
        new JSONObject()
            .put("peer", "iris")
            .put("display", "Iris")
            .put("last_at", "2026-10-28T09:12:04Z")
            .put(
                "last",
                new JSONObject()
                    .put("body", "")
                    .put(
                        "attachments",
                        new JSONArray()
                            .put(new JSONObject().put("id", "att_1").put("filename", "Quarterly report.pdf"))
                            .put(new JSONObject().put("id", "att_2").put("filename", "b.png")))));
    conversations.put(
        new JSONObject()
            .put("peer", "ultron")
            .put("last_at", "2026-10-28T08:00:00Z")
            .put(
                "last",
                new JSONObject()
                    .put("body", "All quiet")
                    .put(
                        "attachments",
                        new JSONArray()
                            .put(new JSONObject().put("id", "att_3").put("filename", "c.png")))));
    List<Model.Conversation> rows = Model.conversations(conversations, null);
    assertEquals("2 files", rows.get(0).preview);
    assertEquals("All quiet", rows.get(1).preview);
    assertEquals("Quarterly report.pdf", Model.filePreview(new JSONObject().put("attachments", new JSONArray().put(new JSONObject().put("id", "a").put("filename", "Quarterly report.pdf")))));
  }

  @Test
  public void malformedAttachmentsAreIgnored() throws Exception {
    JSONArray array = new JSONArray();
    array.put(JSONObject.NULL);
    array.put(new JSONObject().put("filename", "no id.pdf"));
    array.put(new JSONObject().put("id", "att_ok").put("filename", "ok.pdf").put("size", 10));
    List<Attachment> list = Attachment.list(array);
    assertEquals(1, list.size());
    assertEquals("att_ok", list.get(0).id);
    assertTrue(Attachment.list(null).isEmpty());
  }

  @Test
  public void nightModeArithmeticKeepsOtherUiBits() {
    int base = android.content.res.Configuration.UI_MODE_TYPE_NORMAL;
    assertEquals(
        base | android.content.res.Configuration.UI_MODE_NIGHT_YES,
        ThemeMode.applyNightMode(base, ThemeMode.DARK));
    assertEquals(
        base | android.content.res.Configuration.UI_MODE_NIGHT_NO,
        ThemeMode.applyNightMode(base, ThemeMode.LIGHT));
    assertEquals(base, ThemeMode.applyNightMode(base, ThemeMode.SYSTEM));
    int alreadyDark = base | android.content.res.Configuration.UI_MODE_NIGHT_YES;
    assertEquals(
        base | android.content.res.Configuration.UI_MODE_NIGHT_NO,
        ThemeMode.applyNightMode(alreadyDark, ThemeMode.LIGHT));
  }
}
