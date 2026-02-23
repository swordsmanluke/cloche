# frozen_string_literal: true

class Calculator
  class Error < StandardError; end
  class InvalidTokenError < Error; end
  class MismatchedParenthesesError < Error; end
  class DivisionByZeroError < Error; end

  def evaluate(expression)
    tokens = tokenize(expression)
    raise InvalidTokenError, 'Empty expression' if tokens.empty?

    parser = Parser.new(tokens)
    ast = parser.parse_expression
    unless parser.at_end?
      token = parser.current_token
      raise MismatchedParenthesesError, "Unexpected ')'" if token[:type] == :rparen

      raise InvalidTokenError, "Unexpected token: #{token[:value]}"
    end

    evaluate_ast(ast).to_f
  end

  private

  def tokenize(expression)
    tokens = []
    i = 0

    while i < expression.length
      char = expression[i]

      if char.match?(/\s/)
        i += 1
        next
      end

      if char.match?(/\d/) || (char == '.' && i + 1 < expression.length && expression[i + 1].match?(/\d/))
        num_str, i = parse_number(expression, i)
        tokens << { type: :number, value: num_str.to_f }
        next
      end

      i = tokenize_symbol(tokens, char, i)
    end

    tokens
  end

  def parse_number(expression, pos)
    num_str = +''
    has_dot = false

    while pos < expression.length && (expression[pos].match?(/\d/) || expression[pos] == '.')
      if expression[pos] == '.'
        raise InvalidTokenError, 'Invalid number: multiple decimal points' if has_dot

        has_dot = true
      end
      num_str << expression[pos]
      pos += 1
    end

    [num_str, pos]
  end

  def tokenize_symbol(tokens, char, pos)
    case char
    when '+', '-', '*', '/'
      tokens << { type: :operator, value: char }
    when '('
      tokens << { type: :lparen, value: '(' }
    when ')'
      tokens << { type: :rparen, value: ')' }
    else
      raise InvalidTokenError, "Invalid character: '#{char}'"
    end

    pos + 1
  end

  def evaluate_ast(node)
    case node[:type]
    when :number
      node[:value]
    when :unary
      -evaluate_ast(node[:operand])
    when :binary
      evaluate_binary(node)
    end
  end

  def evaluate_binary(node)
    left = evaluate_ast(node[:left])
    right = evaluate_ast(node[:right])

    case node[:operator]
    when '+'
      left + right
    when '-'
      left - right
    when '*'
      left * right
    when '/'
      raise DivisionByZeroError, 'Division by zero' if right.zero?

      left / right
    end
  end

  class Parser
    def initialize(tokens)
      @tokens = tokens
      @pos = 0
    end

    def current_token
      @tokens[@pos]
    end

    def at_end?
      @pos >= @tokens.length
    end

    def parse_expression
      parse_additive
    end

    private

    def advance
      token = @tokens[@pos]
      @pos += 1
      token
    end

    def expect(type)
      token = current_token
      if token.nil? || token[:type] != type
        expected = type == :rparen ? ')' : type.to_s
        raise MismatchedParenthesesError, "Expected '#{expected}'" if type == :rparen

        raise InvalidTokenError, "Expected #{expected}"
      end
      advance
    end

    def parse_additive
      left = parse_multiplicative

      while !at_end? && current_token[:type] == :operator && ['+', '-'].include?(current_token[:value])
        operator = advance[:value]
        right = parse_multiplicative
        left = { type: :binary, operator: operator, left: left, right: right }
      end

      left
    end

    def parse_multiplicative
      left = parse_unary

      while !at_end? && current_token[:type] == :operator && ['*', '/'].include?(current_token[:value])
        operator = advance[:value]
        right = parse_unary
        left = { type: :binary, operator: operator, left: left, right: right }
      end

      left
    end

    def parse_unary
      if !at_end? && current_token[:type] == :operator && current_token[:value] == '-'
        advance
        operand = parse_unary
        return { type: :unary, operand: operand }
      end

      parse_primary
    end

    def parse_primary
      raise InvalidTokenError, 'Unexpected end of expression' if at_end?

      token = current_token

      case token[:type]
      when :number
        advance
        { type: :number, value: token[:value] }
      when :lparen
        advance
        expr = parse_additive
        expect(:rparen)
        expr
      when :rparen
        raise MismatchedParenthesesError, "Unexpected ')'"
      else
        raise InvalidTokenError, "Unexpected token: '#{token[:value]}'"
      end
    end
  end
end
