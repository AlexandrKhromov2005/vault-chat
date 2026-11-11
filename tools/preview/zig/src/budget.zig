const std = @import("std");

pub const max_input = 10 << 20;
pub const max_pixels = 24_000_000;
pub const max_decoded = 100 << 20;

pub const Format = enum { png, jpeg };
pub const Info = struct { format: Format, width: u32, height: u32 };
pub const Error = error{ InvalidImage, Skipped };

pub fn inspect(data: []const u8) Error!Info {
    if (data.len >= 29 and std.mem.eql(u8, data[0..8], &.{ 0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a }) and
        std.mem.eql(u8, data[12..16], "IHDR"))
    {
        const w = std.mem.readInt(u32, data[16..20], .big);
        const h = std.mem.readInt(u32, data[20..24], .big);
        try dimensions(w, h);
        var bytes_per_pixel: u64 = 4;
        if (data[24] == 16) bytes_per_pixel = 8;
        var needed = @as(u64, w) * @as(u64, h) * bytes_per_pixel;
        if (data[28] == 1) needed *= 2;
        needed += @as(u64, w) * (2 * bytes_per_pixel + 3);
        if (needed > max_decoded) return error.Skipped;
        return .{ .format = .png, .width = w, .height = h };
    }
    if (data.len >= 4 and data[0] == 0xff and data[1] == 0xd8) {
        if (!jpegComplete(data)) return error.InvalidImage;
        const frame = try jpegFrame(data);
        const sof = frame.payload;
        if (sof.len < 6) return error.InvalidImage;
        const components_count = sof[5];
        if (components_count != 1 and components_count != 3 and components_count != 4) return error.InvalidImage;
        if (6 + @as(usize, components_count) * 3 > sof.len) return error.InvalidImage;
        const w: u32 = std.mem.readInt(u16, sof[3..5], .big);
        const h: u32 = std.mem.readInt(u16, sof[1..3], .big);
        try dimensions(w, h);
        const components = sof[6 .. 6 + @as(usize, components_count) * 3];
        var hmax: u64 = 1;
        var vmax: u64 = 1;
        var i: usize = 1;
        while (i < components.len) : (i += 3) {
            const hs: u64 = components[i] >> 4;
            const vs: u64 = components[i] & 15;
            if (hs == 0 or vs == 0) return error.InvalidImage;
            hmax = @max(hmax, hs);
            vmax = @max(vmax, vs);
        }
        const W: u64 = w;
        const H: u64 = h;
        const mcu_w = (W + 8 * hmax - 1) / (8 * hmax);
        const mcu_h = (H + 8 * vmax - 1) / (8 * vmax);
        const bytes_per_pixel: u64 = if (components_count == 1) 1 else if (components_count == 3) 3 else 8;
        var needed = bytes_per_pixel * mcu_w * 8 * hmax * mcu_h * 8 * vmax;
        if (frame.marker == 0xc2) {
            i = 1;
            while (i < components.len) : (i += 3) {
                needed += 4 * mcu_w * 8 * @as(u64, components[i] >> 4) * mcu_h * 8 * @as(u64, components[i] & 15);
            }
        }
        needed += 3 * W;
        if (needed > max_decoded) return error.Skipped;
        return .{ .format = .jpeg, .width = w, .height = h };
    }
    return error.InvalidImage;
}

fn dimensions(w: u32, h: u32) Error!void {
    if (w == 0 or h == 0) return error.InvalidImage;
    if (@as(u64, w) * @as(u64, h) > max_pixels) return error.Skipped;
}

fn jpegComplete(data: []const u8) bool {
    var i: usize = 2;
    var in_scan = false;
    while (i + 1 < data.len) {
        if (in_scan) {
            if (data[i] != 0xff) {
                i += 1;
                continue;
            }
            const marker = data[i + 1];
            if (marker == 0xff) {
                i += 1;
                continue;
            }
            if (marker == 0x00 or (marker >= 0xd0 and marker <= 0xd7)) {
                i += 2;
                continue;
            }
            if (marker == 0xd9) return true;
            in_scan = false;
            continue;
        }
        if (i + 4 > data.len or data[i] != 0xff) return false;
        const marker = data[i + 1];
        if (marker == 0xff) {
            i += 1;
            continue;
        }
        if (marker == 0xd9) return true;
        if (marker == 0x01 or (marker >= 0xd0 and marker <= 0xd8)) {
            i += 2;
            continue;
        }
        const length = std.mem.readInt(u16, data[i + 2 ..][0..2], .big);
        const end = i + 2 + @as(usize, length);
        if (length < 2 or end < i + 4 or end > data.len) return false;
        i = end;
        if (marker == 0xda) in_scan = true;
    }
    return false;
}

const Frame = struct { marker: u8, payload: []const u8 };

fn jpegFrame(data: []const u8) Error!Frame {
    var i: usize = 2;
    while (i + 4 <= data.len) {
        if (data[i] != 0xff) return error.InvalidImage;
        const marker = data[i + 1];
        if (marker == 0xff) {
            i += 1;
            continue;
        }
        if (marker == 0x01 or (marker >= 0xd0 and marker <= 0xd8)) {
            i += 2;
            continue;
        }
        const length = std.mem.readInt(u16, data[i + 2 ..][0..2], .big);
        const end = i + 2 + @as(usize, length);
        if (length < 2 or end < i + 4 or end > data.len) return error.InvalidImage;
        if (marker >= 0xc0 and marker <= 0xc2) {
            return .{ .marker = marker, .payload = data[i + 4 .. end] };
        }
        i = end;
    }
    return error.InvalidImage;
}

test "invalid and oversized inputs are separated" {
    try std.testing.expectError(error.InvalidImage, inspect("not an image"));
    var png: [29]u8 = @splat(0);
    @memcpy(png[0..8], &[_]u8{ 0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a });
    @memcpy(png[12..16], "IHDR");
    std.mem.writeInt(u32, png[16..20], 6000, .big);
    std.mem.writeInt(u32, png[20..24], 5000, .big);
    try std.testing.expectError(error.Skipped, inspect(&png));
}

test "PNG memory budget includes sixteen-bit and interlace costs" {
    var png: [29]u8 = @splat(0);
    @memcpy(png[0..8], &[_]u8{ 0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a });
    @memcpy(png[12..16], "IHDR");
    std.mem.writeInt(u32, png[16..20], 4000, .big);
    std.mem.writeInt(u32, png[20..24], 4000, .big);
    png[24] = 16;
    png[28] = 1;
    try std.testing.expectError(error.Skipped, inspect(&png));
    std.mem.writeInt(u32, png[16..20], 2000, .big);
    std.mem.writeInt(u32, png[20..24], 2000, .big);
    const info = try inspect(&png);
    try std.testing.expectEqual(Format.png, info.format);
}
