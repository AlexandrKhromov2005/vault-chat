// Wording of failed requests for the user. Server messages are returned as
// plain text for the caller to insert with textContent.

export function describeError(err) {
  switch (err.code) {
    case 'network':
      return 'Нет связи с сервером. Проверьте, что Gateway запущен.';
    case 'rate_limited':
      return 'Слишком много запросов. Подождите немного и повторите.';
    case 'not_found':
      return 'Не найдено, или у вас нет доступа.';
    case 'permission_denied':
      return 'Недостаточно прав для этого действия.';
    case 'failed_precondition':
      return 'Владелец не может покинуть канал или быть из него удалён.';
    case 'unavailable':
    case 'timeout':
      return 'Сервис временно недоступен. Повторите позже.';
    case 'already_exists':
      // Registration conflicts; a reused message ID keeps its own message.
      if (err.message === 'username already taken') return 'Это имя пользователя уже занято.';
      if (err.message === 'email already registered') return 'Этот email уже зарегистрирован.';
      return err.message;
    case 'invalid_argument':
      return `Проверьте введённые данные: ${err.message}.`;
    default:
      return err.message || 'Что-то пошло не так.';
  }
}
