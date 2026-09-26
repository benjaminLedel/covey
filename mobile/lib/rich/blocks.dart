/// The blocks of a note (#344): what the editor shows and what Markdown
/// stores. The note's body stays Markdown — the format the web, the summaries
/// and, per spec/14, the agents read — and this is the subset the editor
/// knows: paragraphs, headings, bulleted, numbered and checklist items,
/// quotes, dividers, tables and pictures — and, since #371, toggles
/// (`<details><summary>…</summary> … </details>`) and callouts (a quote
/// that begins with an emoji, `> 💡 …`), both chosen so that any other
/// Markdown reader still shows something sensible.
///
/// One line is one block. That is what keeps plain text — a transcript, a
/// note typed before this editor existed — exactly as it was through a
/// parse and a write: every line comes back as it went in, empty lines
/// included. Inline markup (**bold**, _italic_, [links](…)) stays in a
/// block's text as Markdown; the editor styles it (inline.dart).
enum BlockKind {
  paragraph,
  heading1,
  heading2,
  heading3,
  bullet,
  numbered,
  todo,
  quote,
  callout,
  toggle,
  divider,
  table,
  image,
}

class Block {
  Block(
    this.kind, {
    this.text = '',
    this.checked = false,
    this.rows,
    this.ref = '',
    this.emoji = '💡',
    this.body = '',
    this.open = false,
  });

  BlockKind kind;
  String text;

  /// A checklist item's state.
  bool checked;

  /// A table's cells, row by row; the first row is the header.
  List<List<String>>? rows;

  /// A picture's reference: `covey-media://<id>`, or any URL.
  String ref;

  /// A callout's icon.
  String emoji;

  /// A toggle's content, below its summary ([text]); several lines.
  String body;

  /// Whether a toggle shows its content — for this editing session only.
  bool open;

  bool get isText => kind != BlockKind.divider && kind != BlockKind.table && kind != BlockKind.image;

  /// A list item continues as its own kind on Enter; everything else goes on
  /// as a paragraph.
  bool get isListItem => kind == BlockKind.bullet || kind == BlockKind.numbered || kind == BlockKind.todo;

  Block copy() => Block(
    kind,
    text: text,
    checked: checked,
    rows: rows?.map((r) => [...r]).toList(),
    ref: ref,
    emoji: emoji,
    body: body,
    open: open,
  );
}

final _heading = RegExp(r'^(#{1,3})\s+(.*)$');
final _todo = RegExp(r'^\s*[-*]\s+\[( |x|X)\]\s?(.*)$');
final _bullet = RegExp(r'^\s*[-*]\s+(.*)$');
final _numbered = RegExp(r'^\s*\d+\.\s+(.*)$');
final _quote = RegExp(r'^>\s?(.*)$');
// A callout: a quote whose text begins with an emoji and a space. The
// analyser does not know the Extended_Pictographic property; the VM does
// (test/blocks_test.dart).
final _callout = RegExp(
  // ignore: valid_regexps
  r'^>\s?((?:\p{Extended_Pictographic}|\p{Regional_Indicator}{2})\uFE0F?)\s+(.*)$',
  unicode: true,
);
final _detailsOpen = RegExp(r'^\s*<details(\s+open)?\s*>\s*(?:<summary>(.*?)</summary>)?\s*$');
final _summary = RegExp(r'^\s*<summary>(.*?)</summary>\s*$');
final _image = RegExp(r'^!\[[^\]]*\]\(([^)\s]+)\)$');
final _divider = RegExp(r'^(-{3,}|\*{3,}|_{3,})$');
final _tableSep = RegExp(r'^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$');

/// A table row's cells. A pipe written as \| belongs to the cell.
List<String> _cells(String line) {
  var s = line.trim();
  if (s.startsWith('|')) s = s.substring(1);
  if (s.endsWith('|') && !s.endsWith(r'\|')) s = s.substring(0, s.length - 1);
  final cells = <String>[];
  final cur = StringBuffer();
  for (var i = 0; i < s.length; i++) {
    if (s[i] == r'\' && i + 1 < s.length && s[i + 1] == '|') {
      cur.write('|');
      i++;
    } else if (s[i] == '|') {
      cells.add(cur.toString().trim());
      cur.clear();
    } else {
      cur.write(s[i]);
    }
  }
  cells.add(cur.toString().trim());
  return cells;
}

/// Markdown → blocks.
List<Block> parseBlocks(String markdown) {
  final lines = markdown.replaceAll('\r\n', '\n').split('\n');
  final out = <Block>[];
  for (var i = 0; i < lines.length; i++) {
    final line = lines[i];
    // A table: a row of cells, a separator row, then more rows.
    if (line.contains('|') && i + 1 < lines.length && _tableSep.hasMatch(lines[i + 1]) && lines[i + 1].contains('-')) {
      final rows = [_cells(line)];
      i += 2;
      while (i < lines.length && lines[i].contains('|') && lines[i].trim().isNotEmpty) {
        rows.add(_cells(lines[i]));
        i++;
      }
      i--;
      final width = rows.map((r) => r.length).reduce((a, b) => a > b ? a : b);
      for (final r in rows) {
        while (r.length < width) {
          r.add('');
        }
      }
      out.add(Block(BlockKind.table, rows: rows));
      continue;
    }
    RegExpMatch? m;
    // A toggle: <details>, its <summary>, the content, </details>.
    if ((m = _detailsOpen.firstMatch(line)) != null) {
      final end = lines.indexWhere((l) => l.trim() == '</details>', i + 1);
      if (end > 0) {
        var summary = m![2];
        var from = i + 1;
        if (summary == null && from < end) {
          final sm = _summary.firstMatch(lines[from]);
          if (sm != null) {
            summary = sm[1];
            from++;
          }
        }
        final content = lines.sublist(from, end);
        while (content.isNotEmpty && content.first.trim().isEmpty) {
          content.removeAt(0);
        }
        while (content.isNotEmpty && content.last.trim().isEmpty) {
          content.removeLast();
        }
        out.add(Block(BlockKind.toggle, text: summary ?? '', body: content.join('\n')));
        i = end;
        continue;
      }
    }
    if ((m = _image.firstMatch(line.trim())) != null) {
      out.add(Block(BlockKind.image, ref: m![1]!));
    } else if (_divider.hasMatch(line.trim())) {
      out.add(Block(BlockKind.divider));
    } else if ((m = _heading.firstMatch(line)) != null) {
      final level = m![1]!.length;
      out.add(
        Block(
          level == 1
              ? BlockKind.heading1
              : level == 2
              ? BlockKind.heading2
              : BlockKind.heading3,
          text: m[2]!,
        ),
      );
    } else if ((m = _todo.firstMatch(line)) != null) {
      out.add(Block(BlockKind.todo, text: m![2]!, checked: m[1] != ' '));
    } else if ((m = _bullet.firstMatch(line)) != null) {
      out.add(Block(BlockKind.bullet, text: m![1]!));
    } else if ((m = _numbered.firstMatch(line)) != null) {
      out.add(Block(BlockKind.numbered, text: m![1]!));
    } else if ((m = _callout.firstMatch(line)) != null) {
      out.add(Block(BlockKind.callout, emoji: m![1]!, text: m[2]!));
    } else if ((m = _quote.firstMatch(line)) != null) {
      out.add(Block(BlockKind.quote, text: m![1]!));
    } else {
      out.add(Block(BlockKind.paragraph, text: line));
    }
  }
  if (out.isEmpty) out.add(Block(BlockKind.paragraph));
  return out;
}

String _escapeCell(String s) => s.replaceAll('|', r'\|').replaceAll('\n', ' ');

/// Blocks → Markdown. Numbered items are renumbered from their run's start,
/// the way a reader counts them.
String writeBlocks(List<Block> blocks) {
  final out = <String>[];
  var number = 0;
  for (final b in blocks) {
    number = b.kind == BlockKind.numbered ? number + 1 : 0;
    switch (b.kind) {
      case BlockKind.paragraph:
        out.add(b.text);
      case BlockKind.heading1:
        out.add('# ${b.text}');
      case BlockKind.heading2:
        out.add('## ${b.text}');
      case BlockKind.heading3:
        out.add('### ${b.text}');
      case BlockKind.bullet:
        out.add('- ${b.text}');
      case BlockKind.numbered:
        out.add('$number. ${b.text}');
      case BlockKind.todo:
        out.add('- [${b.checked ? 'x' : ' '}] ${b.text}');
      case BlockKind.quote:
        out.add('> ${b.text}');
      case BlockKind.callout:
        out.add('> ${b.emoji} ${b.text}');
      case BlockKind.toggle:
        out.add('<details>');
        out.add('<summary>${b.text}</summary>');
        out.add('');
        if (b.body.isNotEmpty) {
          out.add(b.body);
          out.add('');
        }
        out.add('</details>');
      case BlockKind.divider:
        out.add('---');
      case BlockKind.image:
        out.add('![](${b.ref})');
      case BlockKind.table:
        final rows = b.rows ?? const <List<String>>[];
        if (rows.isEmpty) break;
        out.add('| ${rows.first.map(_escapeCell).join(' | ')} |');
        out.add('|${List.filled(rows.first.length, ' --- ').join('|')}|');
        for (final r in rows.skip(1)) {
          out.add('| ${r.map(_escapeCell).join(' | ')} |');
        }
    }
  }
  return out.join('\n');
}
