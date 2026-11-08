const std = @import("std");

pub fn orientation(data: []const u8) u8 {
    if (data.len < 4 or data[0] != 0xff or data[1] != 0xd8) return 1;
    var i: usize = 2;
    while (i + 4 <= data.len) {
        if (data[i] != 0xff) return 1;
        const marker = data[i + 1];
        if (marker == 0xff) {
            i += 1;
            continue;
        }
        if (marker == 0xda or marker == 0xd9) return 1;
        const length = std.mem.readInt(u16, data[i + 2 ..][0..2], .big);
        if (length < 2 or i + 2 + @as(usize, length) > data.len) return 1;
        const segment = data[i + 4 .. i + 2 + @as(usize, length)];
        if (marker == 0xe1 and segment.len >= 6 and std.mem.eql(u8, segment[0..6], "Exif\x00\x00")) {
            return fromTiff(segment[6..]);
        }
        i += 2 + @as(usize, length);
    }
    return 1;
}

fn fromTiff(tiff: []const u8) u8 {
    if (tiff.len < 8) return 1;
    const order: std.builtin.Endian = if (std.mem.eql(u8, tiff[0..4], "MM\x00\x2a"))
        .big
    else if (std.mem.eql(u8, tiff[0..4], "II\x2a\x00"))
        .little
    else
        return 1;
    const offset = std.mem.readInt(u32, tiff[4..8], order);
    if (offset < 8 or @as(u64, offset) + 2 > tiff.len) return 1;
    const start: usize = offset;
    const count = std.mem.readInt(u16, tiff[start..][0..2], order);
    var k: usize = 0;
    while (k < count) : (k += 1) {
        const entry = start + 2 + 12 * k;
        if (entry + 12 > tiff.len) return 1;
        if (std.mem.readInt(u16, tiff[entry..][0..2], order) != 0x0112) continue;
        if (std.mem.readInt(u16, tiff[entry + 2 ..][0..2], order) != 3 or
            std.mem.readInt(u32, tiff[entry + 4 ..][0..4], order) != 1) return 1;
        const value = std.mem.readInt(u16, tiff[entry + 8 ..][0..2], order);
        return if (value >= 1 and value <= 8) @intCast(value) else 1;
    }
    return 1;
}

test "read bounded EXIF orientation" {
    var jpeg: [42]u8 = @splat(0);
    jpeg[0] = 0xff;
    jpeg[1] = 0xd8;
    jpeg[2] = 0xff;
    jpeg[3] = 0xe1;
    std.mem.writeInt(u16, jpeg[4..6], 36, .big);
    @memcpy(jpeg[6..12], "Exif\x00\x00");
    @memcpy(jpeg[12..16], "MM\x00\x2a");
    std.mem.writeInt(u32, jpeg[16..20], 8, .big);
    std.mem.writeInt(u16, jpeg[20..22], 1, .big);
    std.mem.writeInt(u16, jpeg[22..24], 0x0112, .big);
    std.mem.writeInt(u16, jpeg[24..26], 3, .big);
    std.mem.writeInt(u32, jpeg[26..30], 1, .big);
    std.mem.writeInt(u16, jpeg[30..32], 6, .big);
    try std.testing.expectEqual(@as(u8, 6), orientation(&jpeg));
    jpeg[30] = 0;
    jpeg[31] = 9;
    try std.testing.expectEqual(@as(u8, 1), orientation(&jpeg));
}
