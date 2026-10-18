from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

root = Path(__file__).resolve().parents[1] / 'evidence'
font = ImageFont.truetype('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf', 24)
for device, kinds in [('desktop', ['dashboard', 'connections', 'config-editor']),
                      ('mobile', ['dashboard', 'certificates', 'config-editor'])]:
    rows = []
    for kind in kinds:
        pair = []
        for stage in ['before', 'after']:
            image = Image.open(root / f'{stage}-{device}-light-{kind}.png').convert('RGB')
            image.thumbnail((600, 900))
            tile = Image.new('RGB', (image.width, image.height + 46), '#f3f5fa')
            tile.paste(image, (0, 46))
            ImageDraw.Draw(tile).text((14, 10), stage.title() + ' - ' + kind, font=font, fill='#111424')
            pair.append(tile)
        row = Image.new('RGB', (sum(t.width for t in pair) + 12, max(t.height for t in pair)), '#d7dbe7')
        offset = 0
        for tile in pair:
            row.paste(tile, (offset, 0))
            offset += tile.width + 12
        rows.append(row)
    combined = Image.new('RGB', (max(row.width for row in rows), sum(row.height for row in rows) + 24), '#d7dbe7')
    offset = 0
    for row in rows:
        combined.paste(row, (0, offset))
        offset += row.height + 12
    combined.save(root / f'compare-{device}.png', optimize=True)
