from pathlib import Path
import hashlib
import sys

EXPECTED = "594c2fe35d49488b4382dbfaec8f98366defca819d916ac95becf3e75f4200b3"
source = Path(sys.argv[1]).read_bytes()
if hashlib.sha256(source).hexdigest() != EXPECTED:
    raise SystemExit("unexpected stb_image.h checksum")
text = source.decode()
replacements = [
    (
        '   reduced = (stbi_uc *) stbi__malloc(img_len);\n'
        '   if (reduced == NULL) return stbi__errpuc("outofmem", "Out of memory");',
        '   reduced = (stbi_uc *) orig;',
    ),
    (
        '   STBI_FREE(orig);\n   return reduced;',
        '   return reduced;',
    ),
    (
        '   a->out = (stbi_uc *) stbi__malloc_mad3(x, y, output_bytes, 0); // extra bytes to write off the end into',
        '   int in_place = depth >= 8 && raw == a->expanded && x == a->s->img_x && y == a->s->img_y && out_n == s->img_n;\n'
        '   a->out = in_place ? raw : (stbi_uc *) stbi__malloc_mad3(x, y, output_bytes, 0);\n'
        '   if (in_place) a->expanded = NULL;',
    ),
]
for old, new in replacements:
    if text.count(old) != 1:
        raise SystemExit(f"stb_image patch context mismatch: {old!r}")
    text = text.replace(old, new, 1)
output = Path(sys.argv[2])
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(text)
