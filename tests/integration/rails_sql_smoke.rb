# frozen_string_literal: true

require "rails"
require "active_support/notifications"

unless Rails.respond_to?(:error) && Rails.error.respond_to?(:report)
  warn "Rails.error reporter is unavailable"
  exit 90
end

2.times do
  ActiveSupport::Notifications.instrument(
    "sql.active_record",
    sql: "SELECT * FROM orders WHERE id = $1",
    name: "Order Load",
    cached: false,
  ) do
    nil
  end
end

ActiveSupport::Notifications.instrument(
  "sql.active_record",
  sql: "SELECT * FROM cached_orders",
  name: "Order Load",
  cached: true,
) do
  nil
end

puts "target completed"
