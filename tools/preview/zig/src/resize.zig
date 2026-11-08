const std = @import("std");

pub const max_side = 320;
pub const Size = struct { width: usize, height: usize };
pub const Image = struct {
    width: usize,
    height: usize,
    channels: usize,
    pixels: []const u8,
};

pub fn fit(w: usize, h: usize) Size {
    if (w <= max_side and h <= max_side) return .{ .width = w, .height = h };
    if (w >= h) return .{ .width = max_side, .height = @max(1, (h * max_side + w / 2) / w) };
    return .{ .width = @max(1, (w * max_side + h / 2) / h), .height = max_side };
}

pub fn render(allocator: std.mem.Allocator, source: Image, orientation: u8) !Image {
    if (source.channels < 1 or source.channels > 4 or
        source.pixels.len != source.width * source.height * source.channels)
    {
        return error.InvalidImage;
    }
    const shrunk = try shrink(allocator, source);
    errdefer allocator.free(shrunk.pixels);
    const result = try orient(allocator, shrunk, orientation);
    if (result.pixels.ptr != shrunk.pixels.ptr) allocator.free(shrunk.pixels);
    return result;
}

fn shrink(allocator: std.mem.Allocator, source: Image) !Image {
    const sw = source.width;
    const sh = source.height;
    const size = fit(sw, sh);
    const dw = size.width;
    const dh = size.height;
    const out = try allocator.alloc(u8, dw * dh * 3);
    errdefer allocator.free(out);
    const row = try allocator.alloc(u8, sw * 3);
    defer allocator.free(row);
    const line = try allocator.alloc(u64, dw * 3);
    defer allocator.free(line);
    const acc = try allocator.alloc(u64, 2 * dw * 3);
    defer allocator.free(acc);
    @memset(acc, 0);
    const area: u64 = @as(u64, sw) * @as(u64, sh);
    var cur: usize = 0;
    var y: usize = 0;
    while (y < sh) : (y += 1) {
        const vertical = split(y, sh, dh);
        if (vertical.index != cur) {
            flush(out, acc, dw, cur, area);
            cur = vertical.index;
            @memcpy(acc[0 .. dw * 3], acc[dw * 3 ..]);
            @memset(acc[dw * 3 ..], 0);
        }
        readRow(source, y, row);
        @memset(line, 0);
        var x: usize = 0;
        while (x < sw) : (x += 1) {
            const horizontal = split(x, sw, dw);
            var channel: usize = 0;
            while (channel < 3) : (channel += 1) {
                const value: u64 = row[x * 3 + channel];
                line[horizontal.index * 3 + channel] += value * horizontal.weight;
                if (horizontal.weight < dw) {
                    line[horizontal.index * 3 + 3 + channel] += value * (dw - horizontal.weight);
                }
            }
        }
        for (line, 0..) |value, i| {
            acc[i] += value * vertical.weight;
            acc[dw * 3 + i] += value * (dh - vertical.weight);
        }
    }
    flush(out, acc, dw, cur, area);
    return .{ .width = dw, .height = dh, .channels = 3, .pixels = out };
}

const Split = struct { index: usize, weight: u64 };

fn split(i: usize, n: usize, m: usize) Split {
    const j = i * m / n;
    return .{
        .index = j,
        .weight = @min((i + 1) * m, (j + 1) * n) - i * m,
    };
}

fn flush(out: []u8, acc: []const u64, dw: usize, row: usize, area: u64) void {
    var x: usize = 0;
    while (x < dw) : (x += 1) {
        var channel: usize = 0;
        while (channel < 3) : (channel += 1) {
            out[(row * dw + x) * 3 + channel] = @intCast((acc[x * 3 + channel] + area / 2) / area);
        }
    }
}

fn readRow(source: Image, y: usize, row: []u8) void {
    const stride = source.width * source.channels;
    const pixels = source.pixels[y * stride ..][0..stride];
    var x: usize = 0;
    while (x < source.width) : (x += 1) {
        const p = pixels[x * source.channels ..];
        switch (source.channels) {
            1 => {
                row[x * 3] = p[0];
                row[x * 3 + 1] = p[0];
                row[x * 3 + 2] = p[0];
            },
            2 => {
                const a: u32 = p[1];
                const gray = onWhite(p[0], a);
                row[x * 3] = gray;
                row[x * 3 + 1] = gray;
                row[x * 3 + 2] = gray;
            },
            3 => @memcpy(row[x * 3 ..][0..3], p[0..3]),
            4 => {
                const a: u32 = p[3];
                row[x * 3] = onWhite(p[0], a);
                row[x * 3 + 1] = onWhite(p[1], a);
                row[x * 3 + 2] = onWhite(p[2], a);
            },
            else => unreachable,
        }
    }
}

fn onWhite(value: u8, alpha: u32) u8 {
    return @intCast((@as(u32, value) * alpha + 255 * (255 - alpha) + 127) / 255);
}

fn orient(allocator: std.mem.Allocator, source: Image, orientation: u8) !Image {
    if (orientation <= 1 or orientation > 8) return source;
    const w = source.width;
    const h = source.height;
    const size = if (orientation >= 5) Size{ .width = h, .height = w } else Size{ .width = w, .height = h };
    const out = try allocator.alloc(u8, size.width * size.height * 3);
    var y: usize = 0;
    while (y < size.height) : (y += 1) {
        var x: usize = 0;
        while (x < size.width) : (x += 1) {
            const sx: usize, const sy: usize = switch (orientation) {
                2 => .{ w - 1 - x, y },
                3 => .{ w - 1 - x, h - 1 - y },
                4 => .{ x, h - 1 - y },
                5 => .{ y, x },
                6 => .{ y, h - 1 - x },
                7 => .{ w - 1 - y, h - 1 - x },
                8 => .{ w - 1 - y, x },
                else => unreachable,
            };
            const from = (sy * w + sx) * 3;
            const to = (y * size.width + x) * 3;
            @memcpy(out[to..][0..3], source.pixels[from..][0..3]);
        }
    }
    return .{ .width = size.width, .height = size.height, .channels = 3, .pixels = out };
}

test "area average and alpha are independent of decoder" {
    var pixels: [640 * 4]u8 = undefined;
    for (0..640) |x| {
        pixels[x * 4] = if (x % 2 == 0) 255 else 0;
        pixels[x * 4 + 1] = if (x % 2 == 0) 0 else 255;
        pixels[x * 4 + 2] = 0;
        pixels[x * 4 + 3] = 255;
    }
    const result = try render(std.testing.allocator, .{ .width = 640, .height = 1, .channels = 4, .pixels = &pixels }, 1);
    defer std.testing.allocator.free(result.pixels);
    try std.testing.expectEqualSlices(u8, &.{ 128, 128, 0 }, result.pixels[0..3]);
    const transparent = [_]u8{ 255, 0, 0, 0 };
    const white = try render(std.testing.allocator, .{ .width = 1, .height = 1, .channels = 4, .pixels = &transparent }, 1);
    defer std.testing.allocator.free(white.pixels);
    try std.testing.expectEqualSlices(u8, &.{ 255, 255, 255 }, white.pixels);
}

test "fit and orientation preserve the preview contract" {
    try std.testing.expectEqual(Size{ .width = 320, .height = 240 }, fit(640, 480));
    try std.testing.expectEqual(Size{ .width = 2, .height = 1 }, fit(2, 1));
    const pixels = [_]u8{ 255, 0, 0, 0, 255, 0 };
    const result = try render(std.testing.allocator, .{ .width = 2, .height = 1, .channels = 3, .pixels = &pixels }, 2);
    defer std.testing.allocator.free(result.pixels);
    try std.testing.expectEqualSlices(u8, &.{ 0, 255, 0, 255, 0, 0 }, result.pixels);
}
