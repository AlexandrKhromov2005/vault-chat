const std = @import("std");
const budget = @import("budget.zig");
const exif = @import("exif.zig");
const resize = @import("resize.zig");
const c = @import("stb");

pub fn decode(allocator: std.mem.Allocator, data: []const u8) !resize.Image {
    const info = try budget.inspect(data);
    var width: c_int = 0;
    var height: c_int = 0;
    var channels: c_int = 0;
    const decoded = c.stbi_load_from_memory(data.ptr, @intCast(data.len), &width, &height, &channels, 0);
    if (decoded == null) return error.InvalidImage;
    defer c.stbi_image_free(decoded);
    if (width <= 0 or height <= 0 or channels < 1 or channels > 4 or
        @as(u32, @intCast(width)) != info.width or @as(u32, @intCast(height)) != info.height)
    {
        return error.InvalidImage;
    }
    const w: usize = @intCast(width);
    const h: usize = @intCast(height);
    const channel_count: usize = @intCast(channels);
    const pixels = decoded[0 .. w * h * channel_count];
    const rotation = if (info.format == .jpeg) exif.orientation(data) else 1;
    const result = try resize.render(allocator, .{
        .width = w,
        .height = h,
        .channels = channel_count,
        .pixels = pixels,
    }, rotation);
    if (result.width < 1 or result.height < 1 or result.width > resize.max_side or
        result.height > resize.max_side or result.pixels.len != result.width * result.height * 3)
    {
        return error.InvalidImage;
    }
    return result;
}

test "decoder rejects malformed data" {
    try std.testing.expectError(error.InvalidImage, decode(std.testing.allocator, "not an image"));
}

test "decoder returns known independent RGB pixels" {
    const png = [_]u8{
        0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
        0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0xf4, 0x22, 0x7f,
        0x8a, 0x00, 0x00, 0x00, 0x0e, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
        0x1f, 0x04, 0x01, 0x10, 0xf8, 0x03, 0xfd, 0x4e, 0x95, 0xc1, 0x6f, 0x00, 0x00, 0x00, 0x00, 0x49,
        0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
    };
    const result = try decode(std.testing.allocator, &png);
    defer std.testing.allocator.free(result.pixels);
    try std.testing.expectEqual(@as(usize, 2), result.width);
    try std.testing.expectEqual(@as(usize, 1), result.height);
    try std.testing.expectEqualSlices(u8, &.{ 255, 0, 0, 0, 255, 0 }, result.pixels);
}

test "budget, EXIF and resize tests are analyzed" {
    _ = budget;
    _ = exif;
    _ = resize;
}
