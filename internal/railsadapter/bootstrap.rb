# frozen_string_literal: true

require "json"
require "time"

module RunWitnessRailsAdapter
  EVENTS_PATH = ENV["RUNWITNESS_RAILS_EVENTS_PATH"]
  WORKING_DIRECTORY = ENV.fetch("RUNWITNESS_WORKING_DIRECTORY", Dir.pwd)

  module_function

  def write_event(event)
    return if EVENTS_PATH.nil? || EVENTS_PATH.empty?

    File.open(EVENTS_PATH, "a") do |file|
      file.flock(File::LOCK_EX)
      file.puts(JSON.generate(event))
      file.flush
    end
  rescue Exception
    nil
  end

  def safe_value(value, depth = 0)
    return value if value.nil? || value == true || value == false || value.is_a?(Numeric) || value.is_a?(String)
    return value.to_s if value.is_a?(Symbol)
    return value.to_s if depth >= 3

    case value
    when Array
      value.first(100).map { |item| safe_value(item, depth + 1) }
    when Hash
      value.each_with_object({}) do |(key, item), result|
        result[key.to_s] = safe_value(item, depth + 1)
      end
    else
      value.to_s
    end
  rescue Exception
    "<unserializable>"
  end

  def normalized_location(error)
    location = error.backtrace_locations&.first
    return {} unless location

    path = location.absolute_path || location.path
    if path && !WORKING_DIRECTORY.empty?
      prefix = WORKING_DIRECTORY.end_with?(File::SEPARATOR) ? WORKING_DIRECTORY : WORKING_DIRECTORY + File::SEPARATOR
      path = path.delete_prefix(prefix) if path.start_with?(prefix)
    end

    {
      "path" => path,
      "line" => location.lineno,
      "label" => location.base_label,
    }.compact
  rescue Exception
    {}
  end

  def normalize_sql(statement)
    statement.to_s.strip.gsub(/\s+/, " ")
  rescue Exception
    ""
  end

  class Subscriber
    def report(error, handled:, severity:, context:, source: "application")
      location = RunWitnessRailsAdapter.normalized_location(error)
      RunWitnessRailsAdapter.write_event(
        "type" => "error",
        "observed_at" => Time.now.utc.iso8601(9),
        "error_class" => error.class.name.to_s,
        "error_message" => error.message.to_s,
        "handled" => !!handled,
        "severity" => severity.to_s,
        "source" => source.to_s,
        "location" => location,
        "backtrace" => Array(error.backtrace).first(100),
        "context" => RunWitnessRailsAdapter.safe_value(context || {}),
      )
      nil
    rescue Exception
      nil
    end
  end

  def install_error!
    return true if @error_installed
    return false if @error_installing
    return false unless defined?(Rails) && Rails.respond_to?(:error)

    reporter = Rails.error
    return false unless reporter && reporter.respond_to?(:subscribe)

    @error_installing = true
    reporter.subscribe(Subscriber.new)
    @error_installed = true
    write_event(
      "type" => "subscribed",
      "observed_at" => Time.now.utc.iso8601(9),
    )
    true
  rescue Exception
    false
  ensure
    @error_installing = false
  end

  def install_sql!
    return true if @sql_installed
    return false if @sql_installing
    return false unless defined?(ActiveSupport::Notifications)
    return false unless ActiveSupport::Notifications.respond_to?(:subscribe)

    @sql_installing = true
    ActiveSupport::Notifications.subscribe("sql.active_record") do |_name, started, finished, _event_id, payload|
      begin
        payload ||= {}
        cached = !!payload[:cached]
        statement = normalize_sql(payload[:sql])
        query_name = payload[:name].to_s
        ignored_name = ["SCHEMA", "TRANSACTION"].include?(query_name.upcase)
        next if cached || statement.empty? || ignored_name

        duration_ms = begin
          value = (finished - started).to_f * 1000.0
          value.negative? ? 0.0 : value
        rescue Exception
          0.0
        end
        observed_at = begin
          finished.utc.iso8601(9)
        rescue Exception
          Time.now.utc.iso8601(9)
        end

        write_event(
          "type" => "sql",
          "observed_at" => observed_at,
          "sql_statement" => statement,
          "sql_name" => query_name,
          "sql_cached" => false,
          "duration_ms" => duration_ms,
        )
      rescue Exception
        nil
      end
    end
    @sql_installed = true
    true
  rescue Exception
    false
  ensure
    @sql_installing = false
  end

  def install!
    error_installed = install_error!
    install_sql!
    error_installed
  rescue Exception
    false
  end

  module RequireHook
    def require(feature)
      result = super
      RunWitnessRailsAdapter.install!
      result
    end
  end
end

RunWitnessRailsAdapter.install!
Kernel.prepend(RunWitnessRailsAdapter::RequireHook)
