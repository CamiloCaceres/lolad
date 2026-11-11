package dev.caceres.lolad;

import android.os.Handler;
import android.os.Looper;
import android.util.TypedValue;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.annotation.NonNull;
import androidx.recyclerview.widget.DiffUtil;
import androidx.recyclerview.widget.ListAdapter;
import androidx.recyclerview.widget.RecyclerView;
import com.google.android.material.button.MaterialButton;
import java.util.List;

/**
 * Conversation rows. Markdown is parsed once per message id on the shared
 * background executor; binding only ever reads the cached span.
 */
final class ThreadAdapter extends ListAdapter<Model.Message, RecyclerView.ViewHolder> {
  interface Decisions {
    void decision(String askId, String option);
  }

  static final int TYPE_UPDATE = 0;
  static final int TYPE_MESSAGE = 1;
  static final int PAYLOAD_MARKDOWN = 1;
  static final int PAYLOAD_ENABLED = 2;

  private static final DiffUtil.ItemCallback<Model.Message> DIFF =
      new DiffUtil.ItemCallback<Model.Message>() {
        @Override
        public boolean areItemsTheSame(@NonNull Model.Message oldItem, @NonNull Model.Message newItem) {
          return oldItem.id.equals(newItem.id);
        }

        @Override
        public boolean areContentsTheSame(@NonNull Model.Message oldItem, @NonNull Model.Message newItem) {
          return oldItem.body.equals(newItem.body)
              && oldItem.subject.equals(newItem.subject)
              && oldItem.byline.equals(newItem.byline)
              && oldItem.kind.equals(newItem.kind)
              && oldItem.mine == newItem.mine
              && oldItem.answered == newItem.answered
              && (oldItem.choice == null
                  ? newItem.choice == null
                  : oldItem.choice.equals(newItem.choice))
              && oldItem.options.equals(newItem.options)
              && oldItem.pending == newItem.pending
              && oldItem.attachments.equals(newItem.attachments);
        }
      };

  private final Ui ui;
  private final MarkdownCache markdown;
  private final Decisions decisions;
  private final AttachmentViews.Events attachments;
  private ImageCache images;
  private Api api;
  private String peer;
  private boolean enabled = true;
  private boolean scrolling, flushScheduled;
  private final java.util.Set<String> deferred = new java.util.HashSet<>();
  private final Handler handler = new Handler(Looper.getMainLooper());
  private Runnable onFlushed;

  ThreadAdapter(
      Ui ui, MarkdownCache markdown, Decisions decisions, AttachmentViews.Events attachments) {
    super(DIFF);
    this.ui = ui;
    this.markdown = markdown;
    this.decisions = decisions;
    this.attachments = attachments;
    markdown.setListener(this::rendered);
    setHasStableIds(true);
  }

  /** The conversation whose files are rendered, and the client that fetches them. */
  void setContext(Api api, String peer, ImageCache images) {
    this.api = api;
    this.peer = peer;
    this.images = images;
  }

  void setOnFlushed(Runnable onFlushed) {
    this.onFlushed = onFlushed;
  }

  @Override
  public long getItemId(int position) {
    return Model.stableId(getItem(position).id);
  }

  @Override
  public void onViewRecycled(@NonNull RecyclerView.ViewHolder holder) {
    if (holder instanceof MessageHolder && images != null) {
      MessageHolder message = (MessageHolder) holder;
      for (String key : message.imageKeys) images.cancel(key);
      message.imageKeys.clear();
    }
    super.onViewRecycled(holder);
  }

  @Override
  public int getItemViewType(int position) {
    return "update".equals(getItem(position).kind) ? TYPE_UPDATE : TYPE_MESSAGE;
  }

  void setEnabled(boolean enabled) {
    if (this.enabled == enabled) return;
    this.enabled = enabled;
    if (getItemCount() > 0) notifyItemRangeChanged(0, getItemCount(), PAYLOAD_ENABLED);
  }

  void rendered(String key) {
    deferred.add(key);
    if (scrolling || flushScheduled) return;
    flushScheduled = true;
    handler.postDelayed(
        () -> {
          flushScheduled = false;
          flushDeferred();
        },
        32);
  }

  void setScrolling(boolean scrolling) {
    this.scrolling = scrolling;
    if (!scrolling) flushDeferred();
  }

  /** One range notification for every Markdown span that arrived together. */
  private void flushDeferred() {
    if (scrolling || deferred.isEmpty()) return;
    List<Model.Message> rows = getCurrentList();
    int first = -1, last = -1;
    for (int i = 0; i < rows.size(); i++) {
      if (deferred.contains(rows.get(i).markdownKey())) {
        if (first < 0) first = i;
        last = i;
      }
    }
    deferred.clear();
    if (first >= 0) {
      if (onFlushed != null) onFlushed.run();
      notifyItemRangeChanged(first, last - first + 1, PAYLOAD_MARKDOWN);
    }
  }

  @NonNull
  @Override
  public RecyclerView.ViewHolder onCreateViewHolder(@NonNull ViewGroup parent, int viewType) {
    if (viewType == TYPE_UPDATE) {
      TextView text = ui.text("", Ui.BODY - 2, ui.muted, 400, 3);
      text.setTextIsSelectable(true);
      RecyclerView.LayoutParams params =
          new RecyclerView.LayoutParams(
              RecyclerView.LayoutParams.MATCH_PARENT, RecyclerView.LayoutParams.WRAP_CONTENT);
      params.topMargin = ui.dp(12);
      params.bottomMargin = ui.dp(4);
      text.setLayoutParams(params);
      return new UpdateHolder(text);
    }

    LinearLayout group = new LinearLayout(parent.getContext());
    group.setOrientation(LinearLayout.VERTICAL);
    RecyclerView.LayoutParams groupParams =
        new RecyclerView.LayoutParams(
            RecyclerView.LayoutParams.MATCH_PARENT, RecyclerView.LayoutParams.WRAP_CONTENT);
    groupParams.topMargin = ui.dp(16);
    groupParams.bottomMargin = ui.dp(4);
    group.setLayoutParams(groupParams);

    TextView byline = ui.quiet("");
    byline.setPadding(0, 0, 0, ui.dp(6));
    group.addView(byline);

    LinearLayout card = new LinearLayout(parent.getContext());
    card.setOrientation(LinearLayout.VERTICAL);
    card.setPadding(ui.dp(16), ui.dp(12), ui.dp(16), ui.dp(12));
    card.setLayoutParams(
        new LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT));
    group.addView(card);

    TextView subject = ui.text("", 20, ui.ink, 600, 0);
    subject.setVisibility(View.GONE);
    subject.setPadding(0, 0, 0, ui.dp(6));
    card.addView(subject);

    TextView content = ui.text("", Ui.BODY, ui.ink, 400, 3);
    content.setTextIsSelectable(true);
    card.addView(content);

    LinearLayout files = new LinearLayout(parent.getContext());
    files.setOrientation(LinearLayout.VERTICAL);
    files.setVisibility(View.GONE);
    card.addView(files);

    TextView need = ui.text("Waiting on you", Ui.LABEL, ui.attention, 600, 0);
    need.setVisibility(View.GONE);
    need.setPadding(0, ui.dp(12), 0, ui.dp(4));
    card.addView(need);

    LinearLayout options = new LinearLayout(parent.getContext());
    options.setOrientation(LinearLayout.VERTICAL);
    options.setVisibility(View.GONE);
    card.addView(options);

    TextView answered = ui.quiet("");
    answered.setVisibility(View.GONE);
    answered.setPadding(0, ui.dp(10), 0, 0);
    card.addView(answered);

    return new MessageHolder(
        group, byline, card, subject, content, files, need, options, answered);
  }

  @Override
  public void onBindViewHolder(
      @NonNull RecyclerView.ViewHolder holder, int position, @NonNull List<Object> payloads) {
    if (!payloads.isEmpty() && holder instanceof MessageHolder) {
      Model.Message row = getItem(position);
      MessageHolder message = (MessageHolder) holder;
      if (payloads.contains(PAYLOAD_MARKDOWN)) bindMarkdown(message, row);
      if (payloads.contains(PAYLOAD_ENABLED)) bindEnabled(message);
      return;
    }
    onBindViewHolder(holder, position);
  }

  @Override
  public void onBindViewHolder(@NonNull RecyclerView.ViewHolder holder, int position) {
    Model.Message row = getItem(position);
    if (holder instanceof UpdateHolder) {
      UpdateHolder update = (UpdateHolder) holder;
      update.text.setText(row.body);
      update.text.setTextColor(ui.muted);
      return;
    }
    MessageHolder message = (MessageHolder) holder;
    RecyclerView.LayoutParams params = (RecyclerView.LayoutParams) message.itemView.getLayoutParams();
    params.setMarginStart(row.mine ? ui.dp(32) : 0);
    params.setMarginEnd(row.mine ? 0 : ui.dp(16));
    message.itemView.setLayoutParams(params);

    int onCard = row.mine ? ui.onMine : ui.ink;
    message.byline.setText(row.byline);
    message.byline.setTextColor(ui.muted);
    message.card.setBackground(
        ui.outlined(
            row.mine ? ui.mine : ui.paper,
            Ui.CONTENT_RADIUS,
            row.mine ? ui.cardMineHairline : ui.hairline));

    if (row.subject.isEmpty()) {
      message.subject.setVisibility(View.GONE);
    } else {
      message.subject.setVisibility(View.VISIBLE);
      message.subject.setText(row.subject);
      message.subject.setTextColor(onCard);
      message.subject.setTextSize(
          TypedValue.COMPLEX_UNIT_SP, "deliver".equals(row.kind) ? 20 : Ui.BODY);
      message.subject.setTypeface(
          android.graphics.Typeface.create("sans-serif-medium", android.graphics.Typeface.NORMAL));
    }
    message.content.setTextColor(onCard);
    message.content.setVisibility(row.body.trim().isEmpty() ? View.GONE : View.VISIBLE);
    bindMarkdown(message, row);
    bindAttachments(message, row);
    if (!scrolling) prefetchAround(position);

    boolean ask = row.isAsk() && !row.mine;
    boolean answered = ask && row.answered;
    message.need.setVisibility(ask && !answered ? View.VISIBLE : View.GONE);
    message.answered.setVisibility(answered ? View.VISIBLE : View.GONE);
    if (answered) {
      message.answered.setText(row.choice == null || row.choice.isEmpty() ? "Answered" : "You chose " + row.choice);
      message.answered.setTextColor(row.mine ? ui.onMine : ui.muted);
    }
    message.options.setVisibility(ask && !answered ? View.VISIBLE : View.GONE);
    if (ask && !answered) {
      message.options.removeAllViews();
      for (int i = 0; i < row.options.size(); i++) {
        final String option = row.options.get(i);
        MaterialButton button =
            i == 0
                ? ui.primary(option, () -> decisions.decision(row.id, option))
                : ui.quietAction(option, () -> decisions.decision(row.id, option));
        button.setEnabled(enabled);
        LinearLayout.LayoutParams buttonParams =
            new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        buttonParams.topMargin = ui.dp(8);
        message.options.addView(button, buttonParams);
      }
    } else {
      message.options.removeAllViews();
    }
    bindEnabled(message);
  }

  private void bindAttachments(MessageHolder holder, Model.Message row) {
    if (images != null) for (String key : holder.imageKeys) images.cancel(key);
    holder.imageKeys.clear();
    holder.files.removeAllViews();
    if (row.attachments.isEmpty() || images == null) {
      holder.files.setVisibility(View.GONE);
      return;
    }
    holder.files.setVisibility(View.VISIBLE);
    int target = ui.dp(180);
    for (Attachment attachment : row.attachments) {
      View card = AttachmentViews.messageCard(
          holder.files.getContext(),
          ui,
          images,
          api,
          peer,
          row.mine,
          attachment,
          attachments,
          target,
          holder.imageKeys);
      LinearLayout.LayoutParams params =
          new LinearLayout.LayoutParams(
              LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
      params.topMargin = ui.dp(10);
      holder.files.addView(card, params);
    }
  }

  private void prefetchAround(int position) {
    List<Model.Message> rows = getCurrentList();
    for (int i = position + 1; i <= Math.min(rows.size() - 1, position + 4); i++) {
      Model.Message next = rows.get(i);
      if (!"update".equals(next.kind))
        markdown.prefetch(next.markdownKey(), next.body, next.mine);
    }
    for (int i = position - 1; i >= Math.max(0, position - 2); i--) {
      Model.Message previous = rows.get(i);
      if (!"update".equals(previous.kind))
        markdown.prefetch(previous.markdownKey(), previous.body, previous.mine);
    }
  }

  private void bindMarkdown(MessageHolder holder, Model.Message row) {
    CharSequence cached = markdown.cached(row.markdownKey());
    if (cached != null) {
      holder.content.setText(cached);
      return;
    }
    holder.content.setText(row.body);
    markdown.request(row.markdownKey(), row.body, row.mine);
  }

  private void bindEnabled(MessageHolder holder) {
    for (int i = 0; i < holder.options.getChildCount(); i++) {
      View child = holder.options.getChildAt(i);
      if (child instanceof MaterialButton) ((MaterialButton) child).setEnabled(enabled);
    }
  }

  static final class UpdateHolder extends RecyclerView.ViewHolder {
    final TextView text;

    UpdateHolder(TextView text) {
      super(text);
      this.text = text;
    }
  }

  static final class MessageHolder extends RecyclerView.ViewHolder {
    final TextView byline, subject, content, need, answered;
    final LinearLayout card, files, options;
    final List<String> imageKeys = new java.util.ArrayList<>();

    MessageHolder(
        View item,
        TextView byline,
        LinearLayout card,
        TextView subject,
        TextView content,
        LinearLayout files,
        TextView need,
        LinearLayout options,
        TextView answered) {
      super(item);
      this.byline = byline;
      this.card = card;
      this.subject = subject;
      this.content = content;
      this.files = files;
      this.need = need;
      this.options = options;
      this.answered = answered;
    }
  }
}
