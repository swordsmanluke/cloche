# frozen_string_literal: true

require 'minitest/autorun'
require_relative '../lib/calculator'

class CalculatorTest < Minitest::Test
  def setup
    @calc = Calculator.new
  end

  # Basic operations
  def test_addition
    assert_equal 5.0, @calc.evaluate('2 + 3')
  end

  def test_subtraction
    assert_equal 1.0, @calc.evaluate('4 - 3')
  end

  def test_multiplication
    assert_equal 12.0, @calc.evaluate('3 * 4')
  end

  def test_division
    assert_equal 2.5, @calc.evaluate('5 / 2')
  end

  def test_single_number
    assert_equal 42.0, @calc.evaluate('42')
  end

  # Operator precedence
  def test_multiplication_before_addition
    assert_equal 14.0, @calc.evaluate('2 + 3 * 4')
  end

  def test_multiplication_before_subtraction
    assert_equal 2.0, @calc.evaluate('8 - 3 * 2')
  end

  def test_division_before_addition
    assert_equal 5.0, @calc.evaluate('3 + 8 / 4')
  end

  def test_mixed_precedence
    assert_equal 13.0, @calc.evaluate('2 + 3 * 4 - 5 / 5')
  end

  def test_left_to_right_same_precedence
    assert_equal 3.0, @calc.evaluate('10 - 5 - 2')
  end

  def test_left_to_right_multiplication_division
    assert_equal 5.0, @calc.evaluate('20 / 2 / 2')
  end

  # Parentheses
  def test_parentheses_override_precedence
    assert_equal 20.0, @calc.evaluate('(2 + 3) * 4')
  end

  def test_nested_parentheses
    assert_equal 18.0, @calc.evaluate('((2 + 1) * (3 + 3))')
  end

  def test_deeply_nested_parentheses
    assert_equal 3.0, @calc.evaluate('(((3)))')
  end

  def test_complex_parentheses
    assert_equal 16.0, @calc.evaluate('2 * (3 + (4 - 1) * 2 - 1)')
  end

  def test_example_expression
    assert_equal 11.0, @calc.evaluate('2 + 3 * (4 - 1)')
  end

  # Decimal numbers
  def test_decimal_addition
    assert_in_delta 3.7, @calc.evaluate('1.5 + 2.2'), 0.0001
  end

  def test_decimal_multiplication
    assert_in_delta 5.25, @calc.evaluate('1.5 * 3.5'), 0.0001
  end

  def test_decimal_number_alone
    assert_in_delta 3.14, @calc.evaluate('3.14'), 0.0001
  end

  def test_decimal_division
    assert_in_delta 1.5, @calc.evaluate('4.5 / 3'), 0.0001
  end

  # Negative numbers
  def test_negative_number
    assert_equal(-5.0, @calc.evaluate('-5'))
  end

  def test_negative_in_expression
    assert_equal(-3.0, @calc.evaluate('-5 + 2'))
  end

  def test_negative_parenthesized
    assert_equal(-5.0, @calc.evaluate('-(3 + 2)'))
  end

  def test_double_negative
    assert_equal 5.0, @calc.evaluate('--5')
  end

  def test_negative_times_negative
    assert_equal 6.0, @calc.evaluate('-2 * -3')
  end

  def test_subtraction_of_negative
    assert_equal 8.0, @calc.evaluate('5 - -3')
  end

  # Whitespace handling
  def test_no_spaces
    assert_equal 5.0, @calc.evaluate('2+3')
  end

  def test_extra_spaces
    assert_equal 5.0, @calc.evaluate('  2  +  3  ')
  end

  def test_tabs_and_spaces
    assert_equal 5.0, @calc.evaluate("\t2\t+\t3\t")
  end

  def test_spaces_around_parentheses
    assert_equal 10.0, @calc.evaluate('( 2 + 3 ) * 2')
  end

  # Error cases: mismatched parentheses
  def test_unclosed_parenthesis
    assert_raises(Calculator::MismatchedParenthesesError) { @calc.evaluate('(2 + 3') }
  end

  def test_extra_closing_parenthesis
    assert_raises(Calculator::MismatchedParenthesesError) { @calc.evaluate('2 + 3)') }
  end

  def test_empty_parentheses
    assert_raises(Calculator::MismatchedParenthesesError) { @calc.evaluate('()') }
  end

  def test_mismatched_nested
    assert_raises(Calculator::MismatchedParenthesesError) { @calc.evaluate('((2 + 3)') }
  end

  # Error cases: division by zero
  def test_division_by_zero
    assert_raises(Calculator::DivisionByZeroError) { @calc.evaluate('5 / 0') }
  end

  def test_division_by_zero_expression
    assert_raises(Calculator::DivisionByZeroError) { @calc.evaluate('5 / (3 - 3)') }
  end

  # Error cases: invalid tokens
  def test_invalid_character
    assert_raises(Calculator::InvalidTokenError) { @calc.evaluate('2 & 3') }
  end

  def test_empty_expression
    assert_raises(Calculator::InvalidTokenError) { @calc.evaluate('') }
  end

  def test_whitespace_only
    assert_raises(Calculator::InvalidTokenError) { @calc.evaluate('   ') }
  end

  def test_consecutive_operators
    assert_raises(Calculator::InvalidTokenError) { @calc.evaluate('2 + * 3') }
  end

  def test_trailing_operator
    assert_raises(Calculator::InvalidTokenError) { @calc.evaluate('2 + 3 +') }
  end

  def test_multiple_decimal_points
    assert_raises(Calculator::InvalidTokenError) { @calc.evaluate('1.2.3') }
  end

  # All errors inherit from Calculator::Error
  def test_all_errors_are_calculator_errors
    assert_raises(Calculator::Error) { @calc.evaluate('5 / 0') }
    assert_raises(Calculator::Error) { @calc.evaluate('(2 + 3') }
    assert_raises(Calculator::Error) { @calc.evaluate('2 & 3') }
  end

  # Return type
  def test_returns_float
    result = @calc.evaluate('4 + 2')
    assert_instance_of Float, result
  end

  def test_integer_division_returns_float
    result = @calc.evaluate('10 / 3')
    assert_in_delta 3.3333, result, 0.001
  end
end
