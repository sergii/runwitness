# frozen_string_literal: true

require "rails"

unless Rails.respond_to?(:error) && Rails.error.respond_to?(:report)
  warn "Rails.error reporter is unavailable"
  exit 90
end

error = RuntimeError.new("runwitness handled Rails smoke error")
error.set_backtrace([
  File.join(Dir.pwd, "app/services/checkout.rb") + ":17:in `call'",
])

Rails.error.report(
  error,
  handled: true,
  severity: :warning,
  context: { feature: "checkout" },
  source: "application",
)

puts "target completed"
