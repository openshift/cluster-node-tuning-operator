from . import interfaces
from . import controller
from . import dbus_exporter as dbus
from . import unix_socket_exporter as unix_socket

def export(*args, **kwargs):
	"""Decorator, use to mark exportable methods."""
	def wrapper(method):
		method.export_params = [ args, kwargs ]
		return method
	return wrapper

def signal(*args, **kwargs):
	"""Decorator, use to mark exportable signals."""
	def wrapper(method):
		method.signal_params = [ args, kwargs ]
		return method
	return wrapper

def register_exporter(instance):
	if not isinstance(instance, interfaces.ExporterInterface):
		raise Exception()
	ctl = controller.ExportsController.get_instance()
	return ctl.register_exporter(instance)

def register_object(instance):
	if not isinstance(instance, interfaces.ExportableInterface):
		raise Exception()
	ctl = controller.ExportsController.get_instance()
	return ctl.register_object(instance)

def send_signal(*args, **kwargs):
	ctl = controller.ExportsController.get_instance()
	return ctl.send_signal(*args, **kwargs)

def start():
	ctl = controller.ExportsController.get_instance()
	return ctl.start()

def stop():
	ctl = controller.ExportsController.get_instance()
	return ctl.stop()

def period_check():
	ctl = controller.ExportsController.get_instance()
	return ctl.period_check()
