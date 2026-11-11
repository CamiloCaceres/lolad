package dev.caceres.lolad;

import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.annotation.NonNull;
import androidx.recyclerview.widget.DiffUtil;
import androidx.recyclerview.widget.ListAdapter;
import androidx.recyclerview.widget.RecyclerView;
import com.google.android.material.button.MaterialButton;

/** Inbox rows; only visible rows exist, and DiffUtil keeps scroll steady. */
final class ConversationAdapter extends ListAdapter<Model.Conversation, ConversationAdapter.Holder> {
  interface Open {
    void conversation(String peer);
  }

  private static final DiffUtil.ItemCallback<Model.Conversation> DIFF =
      new DiffUtil.ItemCallback<Model.Conversation>() {
        @Override
        public boolean areItemsTheSame(
            @NonNull Model.Conversation oldItem, @NonNull Model.Conversation newItem) {
          return oldItem.peer.equals(newItem.peer);
        }

        @Override
        public boolean areContentsTheSame(
            @NonNull Model.Conversation oldItem, @NonNull Model.Conversation newItem) {
          return oldItem.display.equals(newItem.display)
              && oldItem.preview.equals(newItem.preview)
              && oldItem.lastAt.equals(newItem.lastAt)
              && oldItem.stamp.equals(newItem.stamp)
              && oldItem.openAsks == newItem.openAsks
              && oldItem.unread == newItem.unread;
        }
      };

  private final Ui ui;
  private final Open open;

  ConversationAdapter(Ui ui, Open open) {
    super(DIFF);
    this.ui = ui;
    this.open = open;
    setHasStableIds(true);
  }

  @Override
  public long getItemId(int position) {
    return Model.stableId(getItem(position).peer);
  }

  @NonNull
  @Override
  public Holder onCreateViewHolder(@NonNull ViewGroup parent, int viewType) {
    LinearLayout card = new LinearLayout(parent.getContext());
    card.setOrientation(LinearLayout.VERTICAL);
    card.setPadding(ui.dp(16), ui.dp(16), ui.dp(16), ui.dp(16));
    card.setBackground(
        ui.pressable(ui.outlined(ui.paper, Ui.ROW_RADIUS, ui.hairline), Ui.ROW_RADIUS));
    card.setClickable(true);
    RecyclerView.LayoutParams params =
        new RecyclerView.LayoutParams(
            RecyclerView.LayoutParams.MATCH_PARENT, RecyclerView.LayoutParams.WRAP_CONTENT);
    params.bottomMargin = ui.dp(12);
    card.setLayoutParams(params);

    TextView title = ui.rowTitle("");
    card.addView(title);

    LinearLayout needsRow = new LinearLayout(parent.getContext());
    needsRow.setOrientation(LinearLayout.HORIZONTAL);
    needsRow.setGravity(Gravity.CENTER_VERTICAL);
    needsRow.setPadding(0, ui.dp(6), 0, 0);
    needsRow.setVisibility(View.GONE);
    card.addView(needsRow);

    TextView unread = ui.label("");
    unread.setVisibility(View.GONE);
    unread.setPadding(0, ui.dp(4), 0, 0);
    card.addView(unread);

    TextView preview = ui.text("", Ui.BODY, ui.secondary, 400, 3);
    preview.setMaxLines(2);
    preview.setEllipsize(TextUtils.TruncateAt.END);
    preview.setPadding(0, ui.dp(8), 0, ui.dp(8));
    preview.setVisibility(View.GONE);
    card.addView(preview);

    TextView stamp = ui.quiet("");
    card.addView(stamp);

    MaterialButton openButton = ui.quietAction("Open conversation", null);
    LinearLayout.LayoutParams buttonParams =
        new LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
    buttonParams.topMargin = ui.dp(8);
    openButton.setLayoutParams(buttonParams);
    card.addView(openButton);

    return new Holder(
        card, title, needsRow, unread, preview, stamp, openButton);
  }

  @Override
  public void onBindViewHolder(@NonNull Holder holder, int position) {
    Model.Conversation row = getItem(position);
    holder.title.setText(row.display);
    if (row.openAsks > 0) {
      holder.needsRow.setVisibility(View.VISIBLE);
      holder.needsRow.removeAllViews();
      holder.needsRow.addView(ui.status(questions(row.openAsks), ui.attention));
    } else {
      holder.needsRow.setVisibility(View.GONE);
    }
    if (row.unread > 0) {
      holder.unread.setVisibility(View.VISIBLE);
      holder.unread.setText(row.unread + " unread");
    } else {
      holder.unread.setVisibility(View.GONE);
    }
    if (row.preview.isEmpty()) {
      holder.preview.setVisibility(View.GONE);
    } else {
      holder.preview.setVisibility(View.VISIBLE);
      holder.preview.setText(row.preview);
    }
    holder.stamp.setText(row.stamp);
    holder.itemView.setOnClickListener(v -> open.conversation(row.peer));
    holder.open.setOnClickListener(v -> open.conversation(row.peer));
    holder.open.setContentDescription("Open conversation with " + row.display);
  }

  static String questions(int count) {
    return "Waiting on you · "
        + count
        + " open "
        + (count == 1 ? "question" : "questions");
  }

  static final class Holder extends RecyclerView.ViewHolder {
    final TextView title, unread, preview, stamp;
    final LinearLayout needsRow;
    final MaterialButton open;

    Holder(
        View item,
        TextView title,
        LinearLayout needsRow,
        TextView unread,
        TextView preview,
        TextView stamp,
        MaterialButton open) {
      super(item);
      this.title = title;
      this.needsRow = needsRow;
      this.unread = unread;
      this.preview = preview;
      this.stamp = stamp;
      this.open = open;
    }
  }
}
