const std = @import("std");
const budget = @import("budget.zig");
const decoder = @import("decoder.zig");

pub fn main(init: std.process.Init) u8 {
    const allocator = init.arena.allocator();
    var stdin_buffer: [16 * 1024]u8 = undefined;
    var stdin_reader: std.Io.File.Reader = .init(.stdin(), init.io, &stdin_buffer);
    var input: std.ArrayList(u8) = .empty;
    stdin_reader.interface.appendRemaining(allocator, &input, .limited(budget.max_input + 1)) catch |err| return switch (err) {
        error.StreamTooLong => fail(3, "input exceeds 10 MiB"),
        else => fail(4, "cannot read input"),
    };
    if (input.items.len > budget.max_input) return fail(3, "input exceeds 10 MiB");
    const image = decoder.decode(allocator, input.items) catch |err| return switch (err) {
        error.InvalidImage => fail(2, "invalid image"),
        error.Skipped => fail(3, "image exceeds preview budget"),
        else => fail(4, "decoder failure"),
    };
    var header: [8]u8 = undefined;
    std.mem.writeInt(u32, header[0..4], @intCast(image.width), .big);
    std.mem.writeInt(u32, header[4..8], @intCast(image.height), .big);
    var stdout_buffer: [16 * 1024]u8 = undefined;
    var stdout_writer: std.Io.File.Writer = .init(.stdout(), init.io, &stdout_buffer);
    const writer = &stdout_writer.interface;
    writer.writeAll(&header) catch return fail(4, "cannot write output");
    writer.writeAll(image.pixels) catch return fail(4, "cannot write output");
    writer.flush() catch return fail(4, "cannot write output");
    return 0;
}

fn fail(code: u8, message: []const u8) u8 {
    std.debug.print("preview: {s}\n", .{message});
    return code;
}

test {
    _ = decoder;
}
