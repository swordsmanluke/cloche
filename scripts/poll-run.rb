#!/usr/bin/env ruby

run_id = ARGV[0]
if run_id.nil?
  puts "USAGE: #{$0} <run_id>"
  exit 1
end

loop do
  status = `cloche status #{run_id} 2>&1`.lines.grep(/^State:/).first&.split&.last

  if %w[succeeded failed].include?(status)
    puts "Done. Final state: #{status}"
    exit 0
  else
    print "."
    sleep 10
  end
end
